package images

import (
	"context"
	"testing"
	"time"
)

// M6 (D84): a collection never starts while an image is in use, and image use
// waits for a running collection.
func TestMaintenance(t *testing.T) {
	var m Maintenance
	ctx := t.Context()

	release, err := m.Use(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.TryCollect(); ok {
		t.Fatal("collection started while an image was in use")
	}
	release()
	release() // releasing twice is harmless
	stop, ok := m.TryCollect()
	if !ok {
		t.Fatal("collection refused with nothing in use")
	}
	if _, ok := m.TryCollect(); ok {
		t.Fatal("two collections at once")
	}

	used := make(chan error, 1)
	go func() {
		r, err := m.Use(ctx, nil)
		if err == nil {
			r()
		}
		used <- err
	}()
	select {
	case err := <-used:
		t.Fatalf("image use did not wait for the collection (err %v)", err)
	case <-time.After(50 * time.Millisecond):
	}
	stop()
	select {
	case err := <-used:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("image use still waits after the collection ended")
	}
}

func TestMaintenanceUseCanceled(t *testing.T) {
	var m Maintenance
	stop, _ := m.TryCollect()
	defer stop()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := m.Use(ctx, nil); err == nil {
		t.Fatal("a canceled wait succeeded")
	}
	// The canceled waiter left nothing held.
	stop()
	if again, ok := m.TryCollect(); !ok {
		t.Fatal("collection refused after a canceled wait")
	} else {
		again()
	}
}

func TestMaintenanceNil(t *testing.T) {
	var m *Maintenance
	release, err := m.Use(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if stop, ok := m.TryCollect(); !ok {
		t.Fatal("nil maintenance refused")
	} else {
		stop()
	}
}
