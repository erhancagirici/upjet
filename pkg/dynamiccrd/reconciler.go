package dynamiccrd

import (
	"context"
	"sync"
	"time"

	"github.com/crossplane/crossplane-runtime/pkg/errors"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/ratelimiter"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	ujcontroller "github.com/crossplane/upjet/pkg/controller"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type Reconciler struct {
	client         client.Client
	mgr            manager.Manager
	opts           ujcontroller.Options
	log            logging.Logger
	crdToSetupFn   map[schema.GroupKind]func(ctrl.Manager, ujcontroller.Options) error
	setupFnTracker *SetupFnTracker
}

type SetupFnTracker struct {
	setupFnRegistry     map[schema.GroupKind]bool
	setupFnRegistryLock *sync.Mutex
}

// Option is for configuring the Reconciler object.
type Option func(*Reconciler)

func WithLogger(l logging.Logger) Option {
	return func(r *Reconciler) {
		r.log = l
	}
}

func WithCrdToSetupFn(crdToSetupFn map[schema.GroupKind]func(ctrl.Manager, ujcontroller.Options) error) Option {
	return func(r *Reconciler) {
		r.crdToSetupFn = crdToSetupFn
	}
}

func WithOpts(o ujcontroller.Options) Option {
	return func(r *Reconciler) {
		r.opts = o
	}
}

func NewReconciler(client client.Client, providerMgr manager.Manager, opts ...Option) *Reconciler {
	r := &Reconciler{
		client: client,
		mgr:    providerMgr,
		log:    logging.NewNopLogger(),
		setupFnTracker: &SetupFnTracker{
			setupFnRegistry:     map[schema.GroupKind]bool{},
			setupFnRegistryLock: &sync.Mutex{},
		},
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

func Setup(mgr manager.Manager, crdToSetupFn map[schema.GroupKind]func(ctrl.Manager, ujcontroller.Options) error, nameSuffix string, o ujcontroller.Options) error {
	name := "dynamiccrd/" + nameSuffix

	r := NewReconciler(mgr.GetClient(), mgr,
		WithCrdToSetupFn(crdToSetupFn),
		WithLogger(o.Logger.WithValues("controller", name)),
		WithOpts(o))

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&apiextensionsv1.CustomResourceDefinition{}).
		WithEventFilter(resource.DesiredStateChanged()).
		WithEventFilter(predicate.NewPredicateFuncs(func(object client.Object) bool {
			if crd, ok := object.(*apiextensionsv1.CustomResourceDefinition); ok {
				if crdToSetupFn[schema.GroupKind{Group: crd.Spec.Group, Kind: crd.Spec.Names.Kind}] != nil {
					return true
				}
			}
			return false
		})).
		Complete(ratelimiter.NewReconciler(name, errors.WithSilentRequeueOnConflict(r), o.GlobalRateLimiter))
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.log.WithValues("request", req)
	log.Debug("Reconciling")

	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := r.client.Get(ctx, req.NamespacedName, crd); err != nil {
		if kerrors.IsNotFound(err) {
			// TODO(sergen): Stop the corresponded controller
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, errors.Wrapf(err, "cannot get CRD %q", req.NamespacedName)
	}

	groupKind := schema.GroupKind{Group: crd.Spec.Group, Kind: crd.Spec.Names.Kind}

	if r.crdToSetupFn[groupKind] == nil {
		log.Debug("This CRD was not owned by this provider, skipping...", "crd", req.NamespacedName)
		return reconcile.Result{}, nil
	}

	if !isCRDEstablished(crd) {
		log.Debug("This CRD was not established yet", "crd", req.NamespacedName)
		return reconcile.Result{RequeueAfter: 5 * time.Second}, nil
	}

	r.setupFnTracker.setupFnRegistryLock.Lock()
	defer r.setupFnTracker.setupFnRegistryLock.Unlock()

	if r.setupFnTracker.setupFnRegistry[groupKind] {
		log.Debug("The controller has been already started", "crd", req.NamespacedName)
		return reconcile.Result{}, nil
	}
	providerSetupFn := r.crdToSetupFn[groupKind]
	if err := providerSetupFn(r.mgr, r.opts); err != nil {
		return reconcile.Result{}, errors.Wrapf(err, "cannot start controller for %q CRD", req.NamespacedName)
	}
	r.setupFnTracker.setupFnRegistry[groupKind] = true

	log.Info("The controller started", "crd", req.NamespacedName)
	return reconcile.Result{}, nil
}

func isCRDEstablished(crd *apiextensionsv1.CustomResourceDefinition) bool {
	for _, cond := range crd.Status.Conditions {
		if cond.Type == apiextensionsv1.Established &&
			cond.Status == apiextensionsv1.ConditionTrue {
			return true
		}
	}
	return false
}
