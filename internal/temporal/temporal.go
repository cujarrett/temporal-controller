// Package temporal wraps the few Temporal API calls the controller makes.
package temporal

import (
	"context"
	"errors"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	namespacepb "go.temporal.io/api/namespace/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"google.golang.org/protobuf/types/known/durationpb"
)

// OwnerKey is the namespace data key that names the TemporalNamespace owning it.
const OwnerKey = "temporal.local.lab/owner"

// Namespace is the part of a Temporal namespace the controller manages.
type Namespace struct {
	Name        string
	Description string
	Retention   time.Duration
	// Owner is "<kubernetes namespace>/<name>" of the TemporalNamespace that created it.
	Owner string
}

// Client talks to one Temporal frontend.
type Client struct {
	sdk client.Client
}

// Headers adds gRPC headers to every Temporal call. EntraToken is the real one.
type Headers interface {
	GetHeaders(ctx context.Context) (map[string]string, error)
}

// New returns a client that connects on first use.
// The manager can then start while Temporal is down, and reconciles retry until it is up.
// headers adds a bearer token to every call. Nil sends none.
func New(address string, headers Headers) (*Client, error) {
	sdk, err := client.NewLazyClient(client.Options{HostPort: address, HeadersProvider: headers})
	if err != nil {
		return nil, err
	}
	return &Client{sdk: sdk}, nil
}

// Close releases the connection.
func (c *Client) Close() {
	c.sdk.Close()
}

// Describe returns the namespace, or nil when it does not exist.
func (c *Client) Describe(ctx context.Context, name string) (*Namespace, error) {
	resp, err := c.sdk.WorkflowService().DescribeNamespace(ctx, &workflowservice.DescribeNamespaceRequest{Namespace: name})
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// A deleted namespace lingers for a few seconds while Temporal removes it.
	if resp.GetNamespaceInfo().GetState() == enumspb.NAMESPACE_STATE_DELETED {
		return nil, nil
	}
	return &Namespace{
		Name:        resp.GetNamespaceInfo().GetName(),
		Description: resp.GetNamespaceInfo().GetDescription(),
		Retention:   resp.GetConfig().GetWorkflowExecutionRetentionTtl().AsDuration(),
		Owner:       resp.GetNamespaceInfo().GetData()[OwnerKey],
	}, nil
}

// Register creates the namespace and records its owner.
func (c *Client) Register(ctx context.Context, ns Namespace) error {
	_, err := c.sdk.WorkflowService().RegisterNamespace(ctx, &workflowservice.RegisterNamespaceRequest{
		Namespace:                        ns.Name,
		Description:                      ns.Description,
		WorkflowExecutionRetentionPeriod: durationpb.New(ns.Retention),
		Data:                             map[string]string{OwnerKey: ns.Owner},
	})
	return err
}

// Update sets the description and retention.
// Temporal leaves fields unset in the request as they are.
func (c *Client) Update(ctx context.Context, ns Namespace) error {
	_, err := c.sdk.WorkflowService().UpdateNamespace(ctx, &workflowservice.UpdateNamespaceRequest{
		Namespace:  ns.Name,
		UpdateInfo: &namespacepb.UpdateNamespaceInfo{Description: ns.Description},
		Config:     &namespacepb.NamespaceConfig{WorkflowExecutionRetentionTtl: durationpb.New(ns.Retention)},
	})
	return err
}

// Delete removes the namespace.
// Not found stays an error, because Temporal's cache can miss a namespace created seconds ago.
// The caller retries, rather than dropping its finalizer and orphaning the namespace.
func (c *Client) Delete(ctx context.Context, name string) error {
	_, err := c.sdk.OperatorService().DeleteNamespace(ctx, &operatorservice.DeleteNamespaceRequest{Namespace: name})
	return err
}

func isNotFound(err error) bool {
	var notFound *serviceerror.NamespaceNotFound
	return errors.As(err, &notFound)
}
