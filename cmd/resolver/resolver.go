// SPDX-FileCopyrightText: 2023 The Crossplane Authors <https://crossplane.io>
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pkg/errors"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"
)

const (
	varManagedResource     = "m"
	varManagedResourceList = "l"
	commentFileTransformed = "// Code transformed by upjet. DO NOT EDIT."
)

func transformPackages(apiGroupSuffix, resolverFilePattern string, ignorePackageLoadErrors bool, patterns ...string) error {
	pkgs, err := packages.Load(&packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedImports | packages.NeedDeps | packages.NeedTypes | packages.NeedSyntax,
	}, patterns...)
	if err != nil {
		return errors.Wrapf(err, "failed to load the packages using the patterns %q", strings.Join(patterns, ","))
	}

	for _, p := range pkgs {
		if err := toError(p); err != nil && !ignorePackageLoadErrors {
			return errors.Wrapf(err, "failed to load the package %q", p.Name)
		}
		for i, f := range p.GoFiles {
			if filepath.Base(f) != resolverFilePattern {
				continue
			}
			if err := transformResolverFile(p.Fset, p.Syntax[i], f, strings.Trim(apiGroupSuffix, ".")); err != nil {
				return errors.Wrapf(err, "failed to transform the resolver file %s", f)
			}
		}
	}
	return nil
}

func toError(p *packages.Package) error {
	if p == nil || len(p.Errors) == 0 {
		return nil
	}
	sb := &strings.Builder{}
	for _, e := range p.Errors {
		if _, err := fmt.Fprintln(sb, e); err != nil {
			return errors.Wrap(err, "failed to write the package parse error to the string builder")
		}
	}
	return errors.New(sb.String())
}

type importUsage struct {
	path string
	used bool
}

func addTransformedComment(fset *token.FileSet, node *ast.File) bool {
	cMap := ast.NewCommentMap(fset, node, node.Comments)
	cgl := cMap[node]
	for _, cg := range cgl {
		for _, c := range cg.List {
			if c != nil && c.Text == commentFileTransformed {
				return false
			}
		}
	}
	switch {
	case len(cgl) == 0:
		cgl = []*ast.CommentGroup{
			{
				List: []*ast.Comment{
					{
						Text:  commentFileTransformed,
						Slash: node.FileStart,
					},
				},
			},
		}

	default:
		cgl[0].List = append(cgl[0].List, &ast.Comment{
			Text:  commentFileTransformed,
			Slash: cgl[0].End(),
		})
	}
	cMap[node] = cgl
	return true
}

func transformResolverFile(fset *token.FileSet, node *ast.File, filePath, apiGroupSuffix string) error { //nolint:gocyclo // Arguably, easier to follow
	if !addTransformedComment(fset, node) {
		return nil
	}
	importMap, err := addMRVariableDeclarations(node)
	if err != nil {
		return errors.Wrapf(err, "failed to add the managed resource variable declarations to the file %s", filePath)
	}

	// Map to track imports used in reference.To structs
	importsUsed := make(map[string]importUsage)
	// assign is the assignment statement that assigns the values returned from
	// `APIResolver.Resolve` or `APIResolver.ResolveMultiple` to the local
	// variables in the MR kind's `ResolveReferences` function.
	var assign *ast.AssignStmt
	// block is the MR kind's `ResolveReferences` function's body block.
	// We use this to find the correct place to inject MR variable
	// declarations, calls to the type registry and error checks, etc.
	var block *ast.BlockStmt
	// these are the GVKs for the MR kind and the associated list kind
	var group, version, kind, listKind string

	// traverse the AST loaded from the given source file to remove the
	// cross API-group import statements from it. This helps in avoiding
	// the import cycles related to the cross-resource references.
	var inspectErr error
	ast.Inspect(node, func(n ast.Node) bool {
		switch x := n.(type) {
		// this transformer takes care of removing the unneeded import statements
		// (after the transformation), which are the target cross API-group
		// references we are trying to avoid to prevent import cycles and appear
		// in cross-resource reference targets.
		case *ast.ImportSpec:
			// initially, mark all imports as needed
			key := ""
			if x.Name != nil {
				key = x.Name.Name
			} else {
				key = x.Path.Value
			}
			importsUsed[key] = importUsage{
				path: strings.Trim(x.Path.Value, `"`),
				used: true,
			}

			// keep a hold of the `ResolveReferences` function body so that we can
		// properly inject variable declarations, error checks, etc. into the
		// correct positions.
		case *ast.FuncDecl:
			block = x.Body

			// keep a hold of the `APIResolver.Resolve` and
		// `APIResolver.ResolveMultiple` return value assignments as we will
		// inject code right above it.
		case *ast.AssignStmt:
			assign = x

		// we will attempt to transform expressions such as
		// `reference.To{List: &v1beta1.MRList{}, Managed: &v1beta1.MR{}}`
		// into:
		// `reference.To{List: l, Managed: m}`, where
		// l and m are local variables holding the correctly types MR kind
		// and MR list kind objects as the reference targets.
		// Such expressions are the primary sources of cross API-group
		// import statements.
		// Cross API-group extractors are rare, and they should be
		// handled when they're being added, this transformer does not
		// consider them.
		case *ast.KeyValueExpr:
			// check if the key is "To" and the value is a CompositeLit
			if key, ok := x.Key.(*ast.Ident); ok && key.Name == "To" {
				// prevent a previous GVK from being reused
				group, version, kind, listKind = "", "", "", ""
				if cl, ok := x.Value.(*ast.CompositeLit); ok {
					// check if there are any package qualifiers in the CompositeLit
					for _, elt := range cl.Elts {
						if kv, ok := elt.(*ast.KeyValueExpr); ok {
							if uexpr, ok := kv.Value.(*ast.UnaryExpr); ok {
								if cl, ok := uexpr.X.(*ast.CompositeLit); ok {
									// then the reference target resides in another API group
									// and the composite literal is a selector expression such as
									// v1beta1.MR. In this case, we deduce the GV of the MR and
									// list using the selector expression and the corresponding
									// import statements (with the name as the expression).
									// Kind and list kind are determined from the field selector.
									key := kv.Key.(*ast.Ident).Name
									if sexpr, ok := cl.Type.(*ast.SelectorExpr); ok {
										if ident, ok := sexpr.X.(*ast.Ident); ok {
											path := importsUsed[ident.Name].path
											importsUsed[ident.Name] = importUsage{
												path: path,
												used: false,
											}
											// we will parse the import path such as:
											// github.com/upbound/provider-aws/apis/ec2/v1beta1
											// and extract the GV information from it.
											tokens := strings.Split(path, "/")
											// e.g., v1beta1
											version = tokens[len(tokens)-1]
											// e.g., ec2.aws.upbound.io
											group = fmt.Sprintf("%s.%s", tokens[len(tokens)-2], apiGroupSuffix)
											// extract the kind and list kind names from the field
											// selector.
											if sexpr.Sel != nil {
												if key == "List" {
													listKind = sexpr.Sel.Name
												} else {
													kind = sexpr.Sel.Name
												}
											}
										}
									} else {
										// then the reference target is in the same package as the
										// source. We still transform it for uniformity and
										// in the future, the source and target might still be
										// moved to different packages.
										// The GV information comes from file name in this case:
										// apis/cur/v1beta1/zz_generated.resolvers.go
										tokens := strings.Split(filePath, "/")
										// e.g., v1beta1
										version = tokens[len(tokens)-2]
										// e.g., cur.aws.upbound.io
										group = fmt.Sprintf("%s.%s", tokens[len(tokens)-3], apiGroupSuffix)
										if ident, ok := cl.Type.(*ast.Ident); ok {
											if key == "List" {
												listKind = ident.Name
											} else {
												kind = ident.Name
											}
										}
									}
								}
							}
						}
					}

					// we will error if we could not determine the reference target GVKs
					// for the MR and its list kind.
					if group == "" || version == "" || kind == "" || listKind == "" {
						inspectErr = errors.Errorf("failed to extract the GVKs for the reference targets. Group: %q, Version: %q, Kind: %q, List Kind: %q", group, version, kind, listKind)
						return false
					}

					// replace the value with a CompositeLit of type reference.To
					// It's transformed into:
					// reference.To{List: l, Managed: m}
					x.Value = &ast.CompositeLit{
						Type: &ast.SelectorExpr{
							X:   ast.NewIdent("reference"),
							Sel: ast.NewIdent("To"),
						},
						// here, l & m
						Elts: []ast.Expr{
							&ast.KeyValueExpr{
								Key:   ast.NewIdent("List"),
								Value: ast.NewIdent(varManagedResourceList),
							},
							&ast.KeyValueExpr{
								Key:   ast.NewIdent("Managed"),
								Value: ast.NewIdent(varManagedResource),
							},
						},
					}

					// get the statements including the import statements we need to make
					// calls to the type registry.
					mrImports, stmts := getManagedResourceStatements(group, version, kind, listKind)
					// insert the statements that implement type registry lookups
					if !insertStatements(stmts, block, assign) {
						inspectErr = errors.Errorf("failed to insert the type registry lookup statements for Group: %q, Version: %q, Kind: %q, List Kind: %q", group, version, kind, listKind)
						return false
					}
					// add the new import statements we need to implement the
					// type registry lookups.
					for k, v := range mrImports {
						importMap[k] = v
					}
				}
			}
		}
		return true
	})

	if inspectErr != nil {
		return errors.Wrap(inspectErr, "failed to inspect the resolver file for transformation")
	}

	// remove the imports that are no longer used.
	for _, decl := range node.Decls {
		if gd, ok := decl.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
			var newSpecs []ast.Spec
			for _, spec := range gd.Specs {
				if imp, ok := spec.(*ast.ImportSpec); ok {
					var name string
					if imp.Name != nil {
						name = imp.Name.Name
					} else {
						name = imp.Path.Value
					}
					if usage, exists := importsUsed[name]; !exists || usage.used {
						newSpecs = append(newSpecs, spec)
					}
				}
			}
			gd.Specs = newSpecs

			newImportKeys := make([]string, 0, len(importMap))
			for k := range importMap {
				newImportKeys = append(newImportKeys, k)
			}
			slices.Sort(newImportKeys)

			for _, path := range newImportKeys {
				gd.Specs = append(gd.Specs, &ast.ImportSpec{
					Name: &ast.Ident{
						Name: importMap[path],
					},
					Path: &ast.BasicLit{
						Kind:  token.STRING,
						Value: path,
					},
				})
			}
		}
	}

	// dump the transformed resolver file
	adjustFunctionDocs(node)
	outFile, err := os.Create(filepath.Clean(filePath))
	if err != nil {
		return errors.Wrap(err, "failed to open the resolver file for writing the transformed AST")
	}
	defer func() { _ = outFile.Close() }()

	// write the modified AST back to the resolver file
	return errors.Wrap(format.Node(outFile, fset, node), "failed to dump the transformed AST back into the resolver file")
}

func adjustFunctionDocs(node *ast.File) {
	node.Decls[1].(*ast.FuncDecl).Doc.List[0].Slash = node.Decls[1].(*ast.FuncDecl).Name.Pos()
}

func insertStatements(stmts []ast.Stmt, block *ast.BlockStmt, assign *ast.AssignStmt) bool {
	astutil.Apply(block, nil, func(c *astutil.Cursor) bool {
		n := c.Node()
		if n != assign {
			return true
		}
		c.Replace(&ast.BlockStmt{
			List: append(stmts, assign),
		})
		return false
	})
	return true
}

func addMRVariableDeclarations(f *ast.File) (map[string]string, error) { //nolint:gocyclo
	// prepare the first variable declaration:
	// `var m xpresource.Managed`
	varDecl1 := &ast.GenDecl{
		Tok: token.VAR,
		Specs: []ast.Spec{
			&ast.ValueSpec{
				Names: []*ast.Ident{ast.NewIdent("m")},
				Type: &ast.SelectorExpr{
					X:   ast.NewIdent("xpresource"),
					Sel: ast.NewIdent("Managed"),
				},
			},
		},
	}

	// prepare the second variable declaration:
	// `var l xpresource.ManagedList`
	varDecl2 := &ast.GenDecl{
		Tok: token.VAR,
		Specs: []ast.Spec{
			&ast.ValueSpec{
				Names: []*ast.Ident{ast.NewIdent("l")},
				Type: &ast.SelectorExpr{
					X:   ast.NewIdent("xpresource"),
					Sel: ast.NewIdent("ManagedList"),
				},
			},
		},
	}

	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}

		if fn.Name.Name == "ResolveReferences" && len(fn.Recv.List) > 0 {
			fn.Body.List = append([]ast.Stmt{
				&ast.DeclStmt{Decl: varDecl1},
				&ast.DeclStmt{Decl: varDecl2},
			}, fn.Body.List...)
		}
		return true
	})
	return map[string]string{
		`"github.com/crossplane/crossplane-runtime/pkg/resource"`: "xpresource",
	}, nil
}

func getManagedResourceStatements(group, version, kind, listKind string) (map[string]string, []ast.Stmt) {
	// prepare the assignment statement:
	// `m, l, err = apisresolver.GetManagedResource("group", "version", "kind", "listKind")`
	assignStmt := &ast.AssignStmt{
		Lhs: []ast.Expr{
			ast.NewIdent("m"),
			ast.NewIdent("l"),
			ast.NewIdent("err"),
		},
		Tok: token.ASSIGN,
		Rhs: []ast.Expr{
			&ast.CallExpr{
				Fun: &ast.SelectorExpr{
					X:   ast.NewIdent("apisresolver"),
					Sel: ast.NewIdent("GetManagedResource"),
				},
				Args: []ast.Expr{
					&ast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf(`"%s"`, group)},
					&ast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf(`"%s"`, version)},
					&ast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf(`"%s"`, kind)},
					&ast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf(`"%s"`, listKind)},
				},
			},
		},
	}

	// prepare the if statement:
	// ```
	// if err != nil {
	//   return errors.Wrap(err, "failed to get the reference target managed resource and its list for reference resolution")
	// }
	// ```
	ifStmt := &ast.IfStmt{
		Cond: &ast.BinaryExpr{
			X:  ast.NewIdent("err"),
			Op: token.NEQ,
			Y:  &ast.Ident{Name: "nil"},
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ReturnStmt{
					Results: []ast.Expr{
						&ast.CallExpr{
							Fun: &ast.SelectorExpr{
								X:   ast.NewIdent("errors"),
								Sel: ast.NewIdent("Wrap"),
							},
							Args: []ast.Expr{
								ast.NewIdent("err"),
								&ast.BasicLit{Kind: token.STRING, Value: `"failed to get the reference target managed resource and its list for reference resolution"`},
							},
						},
					},
				},
			},
		},
	}
	return map[string]string{
		`"github.com/upbound/provider-aws/internal/apis"`: "apisresolver",
	}, []ast.Stmt{assignStmt, ifStmt}
}
