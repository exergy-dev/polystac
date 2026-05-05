package backends

import (
	"context"
	"errors"
	"testing"

	"github.com/example/polystac/pkg/repository"
)

type fakeRepo struct{ repository.Repository }

func TestRegisterAndOpen(t *testing.T) {
	t.Cleanup(func() {
		regMu.Lock()
		delete(reg, "fake")
		regMu.Unlock()
	})

	Register("fake", func(ctx context.Context, cfg any) (repository.Repository, error) {
		if cfg != "ok" {
			return nil, errors.New("bad cfg")
		}
		return fakeRepo{}, nil
	})

	r, err := Open(context.Background(), "fake", "ok")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if r == nil {
		t.Fatal("nil repo")
	}

	if _, err := Open(context.Background(), "missing", nil); err == nil {
		t.Error("expected unknown-backend error")
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	t.Cleanup(func() {
		regMu.Lock()
		delete(reg, "dup")
		regMu.Unlock()
	})
	Register("dup", func(ctx context.Context, cfg any) (repository.Repository, error) { return nil, nil })
	defer func() {
		if recover() == nil {
			t.Error("expected panic on duplicate Register")
		}
	}()
	Register("dup", func(ctx context.Context, cfg any) (repository.Repository, error) { return nil, nil })
}
