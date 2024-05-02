/*
Copyright 2020 The Crossplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package schema

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	xpcontroller "github.com/crossplane/crossplane-runtime/pkg/controller"
	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/meta"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/lib/pq"
	"github.com/pkg/errors"

	"github.com/crossplane-contrib/provider-sql/apis/postgresql/v1alpha1"
	"github.com/crossplane-contrib/provider-sql/pkg/clients"
	"github.com/crossplane-contrib/provider-sql/pkg/clients/postgresql"
	"github.com/crossplane-contrib/provider-sql/pkg/clients/xsql"
)

const (
	errTrackPCUsage = "cannot track ProviderConfig usage"
	errGetPC        = "cannot get ProviderConfig"
	errNoSecretRef  = "ProviderConfig does not reference a credentials Secret"
	errGetSecret    = "cannot get credentials Secret"

	errSelectSchema     = "cannot select schema"
	errCreateSchema     = "cannot create schema"
	errAlterSchemaOwner = "cannot alter schema owner"
	errNotSchema        = "managed resource is not a Schema custom resource"
	errNoDatabase       = "database not passed or could not be resolved"
	errDropSchema       = "cannot drop schema"
	maxConcurrency      = 5
)

// Setup adds a controller that reconciles Schema managed resources.
func Setup(mgr ctrl.Manager, o xpcontroller.Options) error {
	name := managed.ControllerName(v1alpha1.SchemaGroupKind)

	t := resource.NewProviderConfigUsageTracker(mgr.GetClient(), &v1alpha1.ProviderConfigUsage{})
	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.SchemaGroupVersionKind),
		managed.WithExternalConnecter(&connector{kube: mgr.GetClient(), usage: t, newDB: postgresql.New}),
		managed.WithLogger(o.Logger.WithValues("controller", name)),
		managed.WithPollInterval(o.PollInterval),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))))

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&v1alpha1.Database{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: maxConcurrency,
		}).
		Complete(r)
}

type connector struct {
	kube  client.Client
	usage resource.Tracker
	newDB func(creds map[string][]byte, database string, sslmode string) xsql.DB
}

func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.Schema)
	if !ok {
		return nil, errors.New(errNotSchema)
	}

	if err := c.usage.Track(ctx, mg); err != nil {
		return nil, errors.Wrap(err, errTrackPCUsage)
	}

	// ProviderConfigReference could theoretically be nil, but in practice the
	// DefaultProviderConfig initializer will set it before we get here.
	pc := &v1alpha1.ProviderConfig{}
	if err := c.kube.Get(ctx, types.NamespacedName{Name: cr.GetProviderConfigReference().Name}, pc); err != nil {
		return nil, errors.Wrap(err, errGetPC)
	}

	// We don't need to check the credentials source because we currently only
	// support one source (PostgreSQLConnectionSecret), which is required and
	// enforced by the ProviderConfig schema.
	ref := pc.Spec.Credentials.ConnectionSecretRef
	if ref == nil {
		return nil, errors.New(errNoSecretRef)
	}

	s := &corev1.Secret{}
	if err := c.kube.Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, s); err != nil {
		return nil, errors.Wrap(err, errGetSecret)
	}

	// TODO: support databaseRef and databaseSelector
	if cr.Spec.ForProvider.Database != nil {
		return &external{db: c.newDB(s.Data, *cr.Spec.ForProvider.Database, clients.ToString(pc.Spec.SSLMode))}, nil
	}

	return &external{db: c.newDB(s.Data, pc.Spec.DefaultDatabase, clients.ToString(pc.Spec.SSLMode))}, nil
}

type external struct{ db xsql.DB }

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.Schema)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotSchema)
	}

	observed := &v1alpha1.SchemaParameters{
		Owner: new(string),
	}

	query := "SELECT schema_owner " +
		"FROM information_schema.schemata WHERE schema_name = $1"

	err := c.db.Scan(ctx,
		xsql.Query{String: query, Parameters: []interface{}{meta.GetExternalName(cr)}},
		observed.Owner,
	)
	if xsql.IsNoRows(err) {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}
	if err != nil {
		return managed.ExternalObservation{}, errors.Wrap(err, errSelectSchema)
	}

	cr.SetConditions(xpv1.Available())

	return managed.ExternalObservation{
		ResourceExists:          true,
		ResourceLateInitialized: lateInit(*observed, &cr.Spec.ForProvider),
		ResourceUpToDate:        upToDate(observed, &cr.Spec.ForProvider),
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.Schema)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotSchema)
	}

	var b strings.Builder
	b.WriteString("CREATE SCHEMA IF NOT EXISTS ")
	b.WriteString(pq.QuoteIdentifier(meta.GetExternalName(cr)))

	// TODO: Add support fo OwnerRef and OwnerSelector
	if cr.Spec.ForProvider.Owner != nil {
		b.WriteString(" AUTHORIZATION ")
		b.WriteString(pq.QuoteIdentifier(*cr.Spec.ForProvider.Owner))
	}

	cr.SetConditions(xpv1.Creating())

	return managed.ExternalCreation{}, errors.Wrap(c.db.Exec(ctx, xsql.Query{String: b.String()}), errCreateSchema)
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.Schema)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotSchema)
	}

	if cr.Spec.ForProvider.Owner != nil {
		query := xsql.Query{String: fmt.Sprintf("ALTER SCHEMA %s OWNER TO %s",
			pq.QuoteIdentifier(meta.GetExternalName(cr)),
			pq.QuoteIdentifier(*cr.Spec.ForProvider.Owner))}
		if err := c.db.Exec(ctx, query); err != nil {
			return managed.ExternalUpdate{}, errors.Wrap(err, errAlterSchemaOwner)
		}
	}

	return managed.ExternalUpdate{}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) error {
	cr, ok := mg.(*v1alpha1.Schema)
	if !ok {
		return errors.New(errNotSchema)
	}

	var b strings.Builder
	b.WriteString("DROP SCHEMA IF EXISTS ")
	b.WriteString(pq.QuoteIdentifier(meta.GetExternalName(cr)))
	b.WriteString(" CASCADE")

	cr.SetConditions(xpv1.Deleting())
	err := c.db.Exec(ctx, xsql.Query{String: b.String()})
	return errors.Wrap(err, errDropSchema)
}

func upToDate(observed *v1alpha1.SchemaParameters, desired *v1alpha1.SchemaParameters) bool {
	if desired.Owner == nil || (observed.Owner != nil && *desired.Owner == *observed.Owner) {
		return true
	}
	return false
}

func lateInit(observed v1alpha1.SchemaParameters, desired *v1alpha1.SchemaParameters) bool {
	li := false

	if desired.Owner == nil && observed.Owner != nil {
		desired.Owner = observed.Owner
		li = true
	}

	return li
}
