// SPDX-FileCopyrightText: 2024 The Crossplane Authors <https://crossplane.io>
//
// SPDX-License-Identifier: Apache-2.0

package conversion

import (
	"reflect"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/test"
	"github.com/google/go-cmp/cmp"
	jsoniter "github.com/json-iterator/go"
	"github.com/pkg/errors"
)

func TestConvert(t *testing.T) {
	type args struct {
		params map[string]any
		paths  []string
		mode   ListConversionMode
		opts   *ConvertOptions
	}
	type want struct {
		err    error
		params map[string]any
	}
	tests := map[string]struct {
		reason string
		args   args
		want   want
	}{
		"NilParamsAndPaths": {
			reason: "Conversion on an nil map should not fail.",
			args:   args{},
		},
		"EmptyPaths": {
			reason: "Empty conversion on a map should be an identity function.",
			args: args{
				params: map[string]any{"a": "b"},
			},
			want: want{
				params: map[string]any{"a": "b"},
			},
		},
		"SingletonListToEmbeddedObject": {
			reason: "Should successfully convert a singleton list at the root level to an embedded object.",
			args: args{
				params: map[string]any{
					"l": []map[string]any{
						{
							"k": "v",
						},
					},
				},
				paths: []string{"l"},
				mode:  ToEmbeddedObject,
			},
			want: want{
				params: map[string]any{
					"l": map[string]any{
						"k": "v",
					},
				},
			},
		},
		"NestedSingletonListsToEmbeddedObjectsPathsInLexicalOrder": {
			reason: "Should successfully convert the parent & nested singleton lists to embedded objects. Paths specified in lexical order.",
			args: args{
				params: map[string]any{
					"parent": []map[string]any{
						{
							"child": []map[string]any{
								{
									"k": "v",
								},
							},
						},
					},
				},
				paths: []string{"parent", "parent[*].child"},
				mode:  ToEmbeddedObject,
			},
			want: want{
				params: map[string]any{
					"parent": map[string]any{
						"child": map[string]any{
							"k": "v",
						},
					},
				},
			},
		},
		"NestedSingletonListsToEmbeddedObjectsPathsInReverseLexicalOrder": {
			reason: "Should successfully convert the parent & nested singleton lists to embedded objects. Paths specified in reverse-lexical order.",
			args: args{
				params: map[string]any{
					"parent": []map[string]any{
						{
							"child": []map[string]any{
								{
									"k": "v",
								},
							},
						},
					},
				},
				paths: []string{"parent[*].child", "parent"},
				mode:  ToEmbeddedObject,
			},
			want: want{
				params: map[string]any{
					"parent": map[string]any{
						"child": map[string]any{
							"k": "v",
						},
					},
				},
			},
		},
		"EmbeddedObjectToSingletonList": {
			reason: "Should successfully convert an embedded object at the root level to a singleton list.",
			args: args{
				params: map[string]any{
					"l": map[string]any{
						"k": "v",
					},
				},
				paths: []string{"l"},
				mode:  ToSingletonList,
			},
			want: want{
				params: map[string]any{
					"l": []map[string]any{
						{
							"k": "v",
						},
					},
				},
			},
		},
		"NestedEmbeddedObjectsToSingletonListInLexicalOrder": {
			reason: "Should successfully convert the parent & nested embedded objects to singleton lists. Paths are specified in lexical order.",
			args: args{
				params: map[string]any{
					"parent": map[string]any{
						"child": map[string]any{
							"k": "v",
						},
					},
				},
				paths: []string{"parent", "parent[*].child"},
				mode:  ToSingletonList,
			},
			want: want{
				params: map[string]any{
					"parent": []map[string]any{
						{
							"child": []map[string]any{
								{
									"k": "v",
								},
							},
						},
					},
				},
			},
		},
		"NestedEmbeddedObjectsToSingletonListInReverseLexicalOrder": {
			reason: "Should successfully convert the parent & nested embedded objects to singleton lists. Paths are specified in reverse-lexical order.",
			args: args{
				params: map[string]any{
					"parent": map[string]any{
						"child": map[string]any{
							"k": "v",
						},
					},
				},
				paths: []string{"parent[*].child", "parent"},
				mode:  ToSingletonList,
			},
			want: want{
				params: map[string]any{
					"parent": []map[string]any{
						{
							"child": []map[string]any{
								{
									"k": "v",
								},
							},
						},
					},
				},
			},
		},
		"FailConversionOfAMultiItemList": {
			reason: `Conversion of a multi-item list in mode "ToEmbeddedObject" should fail.`,
			args: args{
				params: map[string]any{
					"l": []map[string]any{
						{
							"k1": "v1",
						},
						{
							"k2": "v2",
						},
					},
				},
				paths: []string{"l"},
				mode:  ToEmbeddedObject,
			},
			want: want{
				err: errors.Errorf(errFmtMultiItemList, "l", 2),
			},
		},
		"FailConversionOfNonSlice": {
			reason: `Conversion of a non-slice value in mode "ToEmbeddedObject" should fail.`,
			args: args{
				params: map[string]any{
					"l": map[string]any{
						"k": "v",
					},
				},
				paths: []string{"l"},
				mode:  ToEmbeddedObject,
			},
			want: want{
				err: errors.Errorf(errFmtNonSlice, "l", reflect.TypeOf(map[string]any{})),
			},
		},
		"ToSingletonListWithNonExistentPath": {
			reason: `"ToSingletonList" mode conversions specifying only non-existent paths should be identity functions.`,
			args: args{
				params: map[string]any{
					"l": map[string]any{
						"k": "v",
					},
				},
				paths: []string{"nonexistent"},
				mode:  ToSingletonList,
			},
			want: want{
				params: map[string]any{
					"l": map[string]any{
						"k": "v",
					},
				},
			},
		},
		"ToEmbeddedObjectWithNonExistentPath": {
			reason: `"ToEmbeddedObject" mode conversions specifying only non-existent paths should be identity functions.`,
			args: args{
				params: map[string]any{
					"l": []map[string]any{
						{
							"k": "v",
						},
					},
				},
				paths: []string{"nonexistent"},
				mode:  ToEmbeddedObject,
			},
			want: want{
				params: map[string]any{
					"l": []map[string]any{
						{
							"k": "v",
						},
					},
				},
			},
		},
		"WithInjectedKeySingletonListToEmbeddedObject": {
			reason: "Should successfully convert a singleton list at the root level to an embedded object.",
			args: args{
				params: map[string]any{
					"l": []map[string]any{
						{
							"k":     "v",
							"index": "0",
						},
					},
				},
				paths: []string{"l"},
				mode:  ToEmbeddedObject,
				opts: &ConvertOptions{
					ListInjectKeys: map[string]SingletonListInjectKey{
						"l": {
							Key:   "index",
							Value: "0",
						},
					},
				}},
			want: want{
				params: map[string]any{
					"l": map[string]any{
						"k": "v",
					},
				},
			},
		},
		"WithInjectedKeyEmbeddedObjectToSingletonList": {
			reason: "Should successfully convert an embedded object at the root level to a singleton list.",
			args: args{
				params: map[string]any{
					"l": map[string]any{
						"k": "v",
					},
				},
				paths: []string{"l"},
				mode:  ToSingletonList,
				opts: &ConvertOptions{
					ListInjectKeys: map[string]SingletonListInjectKey{
						"l": {
							Key:   "index",
							Value: "0",
						},
					},
				},
			},
			want: want{
				params: map[string]any{
					"l": []map[string]any{
						{
							"k":     "v",
							"index": "0",
						},
					},
				},
			},
		},
		"WithInjectedKeyNestedEmbeddedObjectsToSingletonListInLexicalOrder": {
			reason: "Should successfully convert the parent & nested embedded objects to singleton lists. Paths are specified in lexical order.",
			args: args{
				params: map[string]any{
					"parent": map[string]any{
						"child": map[string]any{
							"k": "v",
						},
					},
				},
				paths: []string{"parent", "parent[*].child"},
				mode:  ToSingletonList,
				opts: &ConvertOptions{
					ListInjectKeys: map[string]SingletonListInjectKey{
						"parent": {
							Key:   "index",
							Value: "0",
						},
						"parent[*].child": {
							Key:   "another",
							Value: "0",
						},
					},
				},
			},
			want: want{
				params: map[string]any{
					"parent": []map[string]any{
						{
							"index": "0",
							"child": []map[string]any{
								{
									"k":       "v",
									"another": "0",
								},
							},
						},
					},
				},
			},
		},
		"WithInjectedKeyNestedSingletonListsToEmbeddedObjectsPathsInLexicalOrder": {
			reason: "Should successfully convert the parent & nested singleton lists to embedded objects. Paths specified in lexical order.",
			args: args{
				params: map[string]any{
					"parent": []map[string]any{
						{
							"index": "0",
							"child": []map[string]any{
								{
									"k":       "v",
									"another": "0",
								},
							},
						},
					},
				},
				paths: []string{"parent", "parent[*].child"},
				mode:  ToEmbeddedObject,
				opts: &ConvertOptions{
					ListInjectKeys: map[string]SingletonListInjectKey{
						"parent": {
							Key:   "index",
							Value: "0",
						},
						"parent[*].child": {
							Key:   "another",
							Value: "0",
						},
					},
				},
			},
			want: want{
				params: map[string]any{
					"parent": map[string]any{
						"child": map[string]any{
							"k": "v",
						},
					},
				},
			},
		},
		// ToSingletonList edge cases
		"NilEmbeddedObjectToSingletonList": {
			reason: "A nil embedded object must convert to an empty list, not a list containing nil.",
			args: args{
				params: map[string]any{"l": nil},
				paths:  []string{"l"},
				mode:   ToSingletonList,
			},
			want: want{
				params: map[string]any{"l": []any{}},
			},
		},
		"ZeroValueObjectToSingletonList": {
			reason: "A non-nil empty embedded object must be wrapped in a singleton list.",
			args: args{
				params: map[string]any{"l": map[string]any{}},
				paths:  []string{"l"},
				mode:   ToSingletonList,
			},
			want: want{
				params: map[string]any{"l": []any{map[string]any{}}},
			},
		},
		"NestedNilChildToSingletonList": {
			reason: "A nil value at a nested path must convert to an empty list at that path.",
			args: args{
				params: map[string]any{"parent": map[string]any{"child": nil}},
				paths:  []string{"parent.child"},
				mode:   ToSingletonList,
			},
			want: want{
				params: map[string]any{"parent": map[string]any{"child": []any{}}},
			},
		},
		"WithInjectedKeyNilValueToSingletonList": {
			reason: "Inject key is silently skipped when embedded object is nil; nil still converts to an empty list.",
			args: args{
				params: map[string]any{"l": nil},
				paths:  []string{"l"},
				mode:   ToSingletonList,
				opts: &ConvertOptions{
					ListInjectKeys: map[string]SingletonListInjectKey{
						"l": {Key: "index", Value: "0"},
					},
				},
			},
			want: want{
				params: map[string]any{"l": []any{}},
			},
		},
		"WildcardWithNilParentToSingletonList": {
			reason: "A wildcard path whose parent value is nil expands to nothing and leaves the map unchanged.",
			args: args{
				params: map[string]any{"parent": nil},
				paths:  []string{"parent[*].child"},
				mode:   ToSingletonList,
			},
			want: want{
				params: map[string]any{"parent": nil},
			},
		},
		// ToEmbeddedObject edge cases
		"EmptySliceToEmbeddedObject": {
			reason: "An empty list must convert to nil, not an empty map, to preserve pointer-field nil semantics.",
			args: args{
				params: map[string]any{"l": []any{}},
				paths:  []string{"l"},
				mode:   ToEmbeddedObject,
			},
			want: want{
				params: map[string]any{"l": nil},
			},
		},
		"SingletonListNilElementToEmbeddedObject": {
			reason: "A singleton list whose only element is null must convert to nil.",
			args: args{
				params: map[string]any{"l": []any{nil}},
				paths:  []string{"l"},
				mode:   ToEmbeddedObject,
			},
			want: want{
				params: map[string]any{"l": nil},
			},
		},
		"SingletonListEmptyMapToEmbeddedObject": {
			reason: "A singleton list containing an empty object must unwrap to an empty map, not nil.",
			args: args{
				params: map[string]any{"l": []any{map[string]any{}}},
				paths:  []string{"l"},
				mode:   ToEmbeddedObject,
			},
			want: want{
				params: map[string]any{"l": map[string]any{}},
			},
		},
		"NilValueAtPathToEmbeddedObject": {
			reason: "A nil value at a path must be left as nil without triggering a non-slice type error.",
			args: args{
				params: map[string]any{"l": nil},
				paths:  []string{"l"},
				mode:   ToEmbeddedObject,
			},
			want: want{
				params: map[string]any{"l": nil},
			},
		},
		"WithInjectedKeyEmptySliceToEmbeddedObject": {
			reason: "An empty list with an inject key must produce nil without panicking on a nil map delete.",
			args: args{
				params: map[string]any{"l": []any{}},
				paths:  []string{"l"},
				mode:   ToEmbeddedObject,
				opts: &ConvertOptions{
					ListInjectKeys: map[string]SingletonListInjectKey{
						"l": {Key: "index", Value: "0"},
					},
				},
			},
			want: want{
				params: map[string]any{"l": nil},
			},
		},
		"WithInjectedKeyNilElementToEmbeddedObject": {
			reason: "A singleton list with a null element and an inject key must produce nil without panicking.",
			args: args{
				params: map[string]any{"l": []any{nil}},
				paths:  []string{"l"},
				mode:   ToEmbeddedObject,
				opts: &ConvertOptions{
					ListInjectKeys: map[string]SingletonListInjectKey{
						"l": {Key: "index", Value: "0"},
					},
				},
			},
			want: want{
				params: map[string]any{"l": nil},
			},
		},
		"WithInjectedKeyNilValueAtPathToEmbeddedObject": {
			reason: "A nil value at a path with an inject key must produce nil without panicking.",
			args: args{
				params: map[string]any{"l": nil},
				paths:  []string{"l"},
				mode:   ToEmbeddedObject,
				opts: &ConvertOptions{
					ListInjectKeys: map[string]SingletonListInjectKey{
						"l": {Key: "index", Value: "0"},
					},
				},
			},
			want: want{
				params: map[string]any{"l": nil},
			},
		},
	}

	for n, tt := range tests {
		t.Run(n, func(t *testing.T) {
			params, err := roundTrip(tt.args.params)
			if err != nil {
				t.Fatalf("Failed to preprocess tt.args.params: %v", err)
			}
			wantParams, err := roundTrip(tt.want.params)
			if err != nil {
				t.Fatalf("Failed to preprocess tt.want.params: %v", err)
			}
			got, err := Convert(params, tt.args.paths, tt.args.mode, tt.args.opts)
			if diff := cmp.Diff(tt.want.err, err, test.EquateErrors()); diff != "" {
				t.Fatalf("\n%s\nConvert(tt.args.params, tt.args.paths): -wantErr, +gotErr:\n%s", tt.reason, diff)
			}
			if diff := cmp.Diff(wantParams, got); diff != "" {
				t.Errorf("\n%s\nConvert(tt.args.params, tt.args.paths): -wantConverted, +gotConverted:\n%s", tt.reason, diff)
			}
		})
	}
}

func TestConvertRoundtrip(t *testing.T) {
	tests := map[string]struct {
		reason  string
		initial map[string]any
		paths   []string
		opts    *ConvertOptions
		want    map[string]any
	}{
		"NilRoundTrip": {
			reason:  "A nil embedded object must survive a ToSingletonList→ToEmbeddedObject roundtrip as nil.",
			initial: map[string]any{"l": nil},
			paths:   []string{"l"},
			want:    map[string]any{"l": nil},
		},
		"EmptyMapRoundTrip": {
			reason:  "An empty embedded object must survive a ToSingletonList→ToEmbeddedObject roundtrip as an empty map.",
			initial: map[string]any{"l": map[string]any{}},
			paths:   []string{"l"},
			want:    map[string]any{"l": map[string]any{}},
		},
		"PopulatedObjectRoundTrip": {
			reason:  "A populated embedded object must survive a ToSingletonList→ToEmbeddedObject roundtrip unchanged.",
			initial: map[string]any{"l": map[string]any{"k": "v"}},
			paths:   []string{"l"},
			want:    map[string]any{"l": map[string]any{"k": "v"}},
		},
		"NestedObjectRoundTrip": {
			reason:  "Nested embedded objects must survive a ToSingletonList→ToEmbeddedObject roundtrip unchanged.",
			initial: map[string]any{
				"parent": map[string]any{
					"child": map[string]any{"k": "v"},
				},
			},
			paths: []string{"parent", "parent[*].child"},
			want: map[string]any{
				"parent": map[string]any{
					"child": map[string]any{"k": "v"},
				},
			},
		},
	}
	for n, tt := range tests {
		t.Run(n, func(t *testing.T) {
			params, err := roundTrip(tt.initial)
			if err != nil {
				t.Fatalf("Failed to preprocess initial params: %v", err)
			}
			wantParams, err := roundTrip(tt.want)
			if err != nil {
				t.Fatalf("Failed to preprocess want params: %v", err)
			}
			asList, err := Convert(params, tt.paths, ToSingletonList, tt.opts)
			if err != nil {
				t.Fatalf("\n%s\nConvert to singleton list: %v", tt.reason, err)
			}
			gotBack, err := Convert(asList, tt.paths, ToEmbeddedObject, tt.opts)
			if err != nil {
				t.Fatalf("\n%s\nConvert back to embedded object: %v", tt.reason, err)
			}
			if diff := cmp.Diff(wantParams, gotBack); diff != "" {
				t.Errorf("\n%s\nRoundtrip: -want, +got:\n%s", tt.reason, diff)
			}
		})
	}
}

func TestConvertRoundtripFromList(t *testing.T) {
	tests := map[string]struct {
		reason  string
		initial map[string]any
		paths   []string
		opts    *ConvertOptions
		want    map[string]any
	}{
		// Symmetric cases: ToEmbeddedObject→ToSingletonList restores the original.
		"EmptySliceRoundTrip": {
			reason:  "An empty singleton list must survive a ToEmbeddedObject→ToSingletonList roundtrip as an empty list (via nil).",
			initial: map[string]any{"l": []any{}},
			paths:   []string{"l"},
			want:    map[string]any{"l": []any{}},
		},
		"PopulatedSingletonListRoundTrip": {
			reason:  "A populated singleton list must survive a ToEmbeddedObject→ToSingletonList roundtrip unchanged.",
			initial: map[string]any{"l": []any{map[string]any{"k": "v"}}},
			paths:   []string{"l"},
			want:    map[string]any{"l": []any{map[string]any{"k": "v"}}},
		},
		"SingletonListEmptyMapRoundTrip": {
			reason:  "A singleton list containing an empty object must survive a ToEmbeddedObject→ToSingletonList roundtrip unchanged.",
			initial: map[string]any{"l": []any{map[string]any{}}},
			paths:   []string{"l"},
			want:    map[string]any{"l": []any{map[string]any{}}},
		},
		"NestedSingletonListsRoundTrip": {
			reason: "Nested singleton lists must survive a ToEmbeddedObject→ToSingletonList roundtrip unchanged.",
			initial: map[string]any{
				"parent": []any{
					map[string]any{
						"child": []any{map[string]any{"k": "v"}},
					},
				},
			},
			paths: []string{"parent", "parent[*].child"},
			want: map[string]any{
				"parent": []any{
					map[string]any{
						"child": []any{map[string]any{"k": "v"}},
					},
				},
			},
		},
		"WithInjectedKeySingletonListRoundTrip": {
			reason: "A singleton list with an injected key must survive a ToEmbeddedObject→ToSingletonList roundtrip: the key is stripped going in and re-injected coming out.",
			initial: map[string]any{"l": []any{map[string]any{"k": "v", "index": "0"}}},
			paths:   []string{"l"},
			opts: &ConvertOptions{
				ListInjectKeys: map[string]SingletonListInjectKey{
					"l": {Key: "index", Value: "0"},
				},
			},
			want: map[string]any{"l": []any{map[string]any{"k": "v", "index": "0"}}},
		},
		// Asymmetric case: a nil element inside the list normalizes during conversion.
		"NilElementSingletonListRoundTrip": {
			reason: "A singleton list with a null element converts to nil embedded object, which then converts back to an empty list — not a list with a null element.",
			initial: map[string]any{"l": []any{nil}},
			paths:   []string{"l"},
			want:    map[string]any{"l": []any{}},
		},
	}
	for n, tt := range tests {
		t.Run(n, func(t *testing.T) {
			params, err := roundTrip(tt.initial)
			if err != nil {
				t.Fatalf("Failed to preprocess initial params: %v", err)
			}
			wantParams, err := roundTrip(tt.want)
			if err != nil {
				t.Fatalf("Failed to preprocess want params: %v", err)
			}
			asObject, err := Convert(params, tt.paths, ToEmbeddedObject, tt.opts)
			if err != nil {
				t.Fatalf("\n%s\nConvert to embedded object: %v", tt.reason, err)
			}
			gotBack, err := Convert(asObject, tt.paths, ToSingletonList, tt.opts)
			if err != nil {
				t.Fatalf("\n%s\nConvert back to singleton list: %v", tt.reason, err)
			}
			if diff := cmp.Diff(wantParams, gotBack); diff != "" {
				t.Errorf("\n%s\nRoundtrip: -want, +got:\n%s", tt.reason, diff)
			}
		})
	}
}

func TestModeString(t *testing.T) {
	tests := map[string]struct {
		m    ListConversionMode
		want string
	}{
		"ToSingletonList": {
			m:    ToSingletonList,
			want: "toSingletonList",
		},
		"ToEmbeddedObject": {
			m:    ToEmbeddedObject,
			want: "toEmbeddedObject",
		},
		"Unknown": {
			m:    ToSingletonList + 1,
			want: "unknown",
		},
	}
	for n, tt := range tests {
		t.Run(n, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, tt.m.String()); diff != "" {
				t.Errorf("String(): -want, +got:\n%s", diff)
			}
		})
	}
}

func roundTrip(m map[string]any) (map[string]any, error) {
	if len(m) == 0 {
		return m, nil
	}
	buff, err := jsoniter.ConfigCompatibleWithStandardLibrary.Marshal(m)
	if err != nil {
		return nil, err
	}
	var r map[string]any
	return r, jsoniter.ConfigCompatibleWithStandardLibrary.Unmarshal(buff, &r)
}
