package temporal

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"go.temporal.io/cloud-sdk/api/cloudservice/v1"
	namespacev1 "go.temporal.io/cloud-sdk/api/namespace/v1"
	resourcev1 "go.temporal.io/cloud-sdk/api/resource/v1"
	"go.temporal.io/cloud-sdk/cloudclient"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// cloudOwnerPrefix starts the description that records a Temporal Cloud namespace's owner.
// Tags would be cleaner, but only account owners and admins can set them.
const cloudOwnerPrefix = "temporal-controller owner: "

// cloudRegion is where every namespace runs. It is fixed once a namespace exists.
const cloudRegion = "aws-us-east-1"

// Cloud talks to Temporal Cloud's management API, the Cloud Ops API.
type Cloud struct {
	api    cloudservice.CloudServiceClient
	client *cloudclient.Client
	// Trust returns the CA that client certificates must chain to, and its SPIFFE trust domain.
	Trust func(ctx context.Context) (Trust, error)
}

// Trust is the SPIRE trust domain and its CA bundle.
type Trust struct {
	CA     []byte
	Domain string
}

// keyFile reads the API key on every call, so a rotated key needs no restart.
type keyFile string

func (k keyFile) GetAPIKey(context.Context) (string, error) {
	b, err := os.ReadFile(string(k))
	if err != nil {
		return "", fmt.Errorf("read Temporal Cloud API key: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// NewCloud returns a Temporal Cloud client that reads its API key from apiKeyFile.
func NewCloud(apiKeyFile string, trust func(ctx context.Context) (Trust, error)) (*Cloud, error) {
	c, err := cloudclient.New(cloudclient.Options{APIKeyReader: keyFile(apiKeyFile)})
	if err != nil {
		return nil, err
	}
	return &Cloud{api: c.CloudService(), client: c, Trust: trust}, nil
}

// Close releases the connection.
func (c *Cloud) Close() {
	_ = c.client.Close()
}

// Describe returns the namespace, or nil when it does not exist.
func (c *Cloud) Describe(ctx context.Context, name string) (*Namespace, error) {
	got, err := c.find(ctx, name)
	if err != nil || got == nil {
		return nil, err
	}
	// A namespace made by hand has no owner prefix, so it reads as ownerless.
	owner, ok := strings.CutPrefix(got.GetSpec().GetDescription(), cloudOwnerPrefix)
	if !ok {
		owner = ""
	}
	ns := &Namespace{
		Name:      name,
		Retention: time.Duration(got.GetSpec().GetRetentionDays()) * 24 * time.Hour,
		Owner:     owner,
		FullName:  got.GetNamespace(),
		Endpoint:  got.GetEndpoints().GetMtlsGrpcAddress(),
		Pending:   got.GetState() != resourcev1.ResourceState_RESOURCE_STATE_ACTIVE,
	}
	trust, err := c.Trust(ctx)
	if err != nil {
		return nil, err
	}
	ns.ClientNamespace = clientNamespace(got.GetSpec(), trust.Domain)
	ns.ClientCAStale = !bytes.Equal(bytes.TrimSpace(got.GetSpec().GetMtlsAuth().GetAcceptedClientCa()), bytes.TrimSpace(trust.CA))
	return ns, nil
}

// Register starts creating the namespace. Temporal Cloud finishes it in the background.
func (c *Cloud) Register(ctx context.Context, ns Namespace) error {
	spec, err := c.spec(ctx, ns)
	if err != nil {
		return err
	}
	_, err = c.api.CreateNamespace(ctx, &cloudservice.CreateNamespaceRequest{Spec: spec})
	return err
}

// Update sets retention, the client CA and which workloads may connect.
func (c *Cloud) Update(ctx context.Context, ns Namespace) error {
	got, err := c.find(ctx, ns.Name)
	if err != nil {
		return err
	}
	if got == nil {
		return fmt.Errorf("namespace %q not found in Temporal Cloud", ns.Name)
	}
	spec, err := c.spec(ctx, ns)
	if err != nil {
		return err
	}
	// The region is fixed at creation, so the update keeps the one it has.
	spec.Replicas = got.GetSpec().GetReplicas()
	_, err = c.api.UpdateNamespace(ctx, &cloudservice.UpdateNamespaceRequest{
		Namespace:       got.GetNamespace(),
		Spec:            spec,
		ResourceVersion: got.GetResourceVersion(),
	})
	return err
}

// Delete starts deleting the namespace. Describe shows it until Temporal Cloud is done.
func (c *Cloud) Delete(ctx context.Context, name string) error {
	got, err := c.find(ctx, name)
	if err != nil {
		return err
	}
	if got == nil {
		return nil
	}
	// A delete already in progress refuses a second one.
	if got.GetState() == resourcev1.ResourceState_RESOURCE_STATE_DELETING {
		return nil
	}
	_, err = c.api.DeleteNamespace(ctx, &cloudservice.DeleteNamespaceRequest{
		Namespace:       got.GetNamespace(),
		ResourceVersion: got.GetResourceVersion(),
	})
	return err
}

// find looks a namespace up by its short name, since the full name holds the account ID.
func (c *Cloud) find(ctx context.Context, name string) (*namespacev1.Namespace, error) {
	resp, err := c.api.GetNamespaces(ctx, &cloudservice.GetNamespacesRequest{Name: name})
	if status.Code(err) == codes.NotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, ns := range resp.GetNamespaces() {
		if ns.GetSpec().GetName() != name {
			continue
		}
		if ns.GetState() == resourcev1.ResourceState_RESOURCE_STATE_DELETED {
			continue
		}
		return ns, nil
	}
	return nil, nil
}

func (c *Cloud) spec(ctx context.Context, ns Namespace) (*namespacev1.NamespaceSpec, error) {
	trust, err := c.Trust(ctx)
	if err != nil {
		return nil, err
	}
	return &namespacev1.NamespaceSpec{
		Name:          ns.Name,
		Replicas:      []*namespacev1.ReplicaSpec{{Region: cloudRegion}},
		RetentionDays: retentionDays(ns.Retention),
		Description:   cloudOwnerPrefix + ns.Owner,
		MtlsAuth: &namespacev1.MtlsAuthSpec{
			Enabled:            true,
			AcceptedClientCa:   trust.CA,
			CertificateFilters: []*namespacev1.CertificateFilterSpec{{SubjectAlternativeName: clientFilter(trust.Domain, ns.ClientNamespace)}},
		},
	}, nil
}

// retentionDays rounds up to whole days, within Temporal Cloud's 1 to 90.
func retentionDays(d time.Duration) int32 {
	days := int32((d + 24*time.Hour - 1) / (24 * time.Hour))
	return max(1, min(90, days))
}

// clientFilter lets in every workload in one Kubernetes namespace.
// Temporal Cloud allows one * at the end of a value.
// The / before it stops namespace foo from matching foo-bar.
func clientFilter(trustDomain, namespace string) string {
	return fmt.Sprintf("spiffe://%s/ns/%s/sa/*", trustDomain, namespace)
}

// clientNamespace reads back the namespace clientFilter wrote.
// Any other filter, or another trust domain, gives "", which the controller treats as drift.
func clientNamespace(spec *namespacev1.NamespaceSpec, trustDomain string) string {
	filters := spec.GetMtlsAuth().GetCertificateFilters()
	if len(filters) != 1 {
		return ""
	}
	prefix := "spiffe://" + trustDomain + "/ns/"
	ns, ok := strings.CutPrefix(filters[0].GetSubjectAlternativeName(), prefix)
	if !ok {
		return ""
	}
	ns, ok = strings.CutSuffix(ns, "/sa/*")
	if !ok || strings.Contains(ns, "/") {
		return ""
	}
	return ns
}

// CloudRetention is the retention Temporal Cloud keeps for d, which counts in whole days.
func CloudRetention(d time.Duration) time.Duration {
	return time.Duration(retentionDays(d)) * 24 * time.Hour
}
