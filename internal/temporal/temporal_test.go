package temporal

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestAgainstServer runs each call against a real Temporal server.
// TEMPORAL_ADDRESS: set it, such as localhost:7233 for temporal server start-dev, or the test skips.
func TestAgainstServer(t *testing.T) {
	address := os.Getenv("TEMPORAL_ADDRESS")
	if address == "" {
		t.Skip("TEMPORAL_ADDRESS not set")
	}
	c, err := New(address, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	name := fmt.Sprintf("temporal-controller-test-%d", time.Now().UnixNano())

	if got, err := c.Describe(ctx, name); err != nil || got != nil {
		t.Fatalf("Describe before Register = %v, %v, want nil, nil", got, err)
	}

	want := Namespace{Name: name, Description: "first", Retention: 24 * time.Hour, Owner: "default/foo"}
	if err := c.Register(ctx, want); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got, err := c.Describe(ctx, name); err != nil || got == nil || *got != want {
		t.Fatalf("Describe after Register = %+v, %v, want %+v", got, err, want)
	}

	want.Description = "second"
	want.Retention = 48 * time.Hour
	// Temporal's namespace cache can take seconds to see a new namespace.
	eventually(t, "Update", func() error { return c.Update(ctx, want) })
	eventually(t, "Describe after Update", func() error {
		got, err := c.Describe(ctx, name)
		if err != nil || got == nil || *got != want {
			return fmt.Errorf("got %+v, %v, want %+v", got, err, want)
		}
		return nil
	})

	eventually(t, "Delete", func() error { return c.Delete(ctx, name) })
	eventually(t, "Describe after Delete", func() error {
		got, err := c.Describe(ctx, name)
		if err != nil || got != nil {
			return fmt.Errorf("got %+v, %v, want nil, nil", got, err)
		}
		return nil
	})
}

func eventually(t *testing.T, what string, try func() error) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		err := try()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: %v", what, err)
		}
		time.Sleep(time.Second)
	}
}
