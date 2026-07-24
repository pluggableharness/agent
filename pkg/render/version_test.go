package render_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/pluggableharness/agent/pkg/render"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

func TestVersionRegistryRenderKnownVersion(t *testing.T) {
	t.Parallel()

	reg := render.NewVersionRegistry()
	reg.Register("v1", func(payload []byte) (*renderv1.RenderTree, error) {
		return render.Tree(render.Text(string(payload))), nil
	})
	reg.Register("v2", func(payload []byte) (*renderv1.RenderTree, error) {
		return render.Tree(render.Code("go", string(payload))), nil
	})

	got, err := reg.Render("v2", []byte("package main"))
	if err != nil {
		t.Fatalf("Render(v2): %v", err)
	}
	block := got.GetRoot().GetCodeBlock()
	if block == nil || block.GetContent() != "package main" {
		t.Errorf("Render(v2) routed to wrong renderer, got root = %v", got.GetRoot())
	}
}

func TestVersionRegistryRenderUnknownVersion(t *testing.T) {
	t.Parallel()

	reg := render.NewVersionRegistry()
	reg.Register("v1", func(payload []byte) (*renderv1.RenderTree, error) {
		return render.Tree(render.Text(string(payload))), nil
	})

	_, err := reg.Render("v99", []byte("x"))
	if !errors.Is(err, render.ErrUnknownSchemaVersion) {
		t.Errorf("Render(v99) error = %v, want wrapping ErrUnknownSchemaVersion", err)
	}
}

func TestVersionRegistryEmpty(t *testing.T) {
	t.Parallel()

	reg := render.NewVersionRegistry()
	_, err := reg.Render("anything", nil)
	if !errors.Is(err, render.ErrUnknownSchemaVersion) {
		t.Errorf("Render on empty registry error = %v, want wrapping ErrUnknownSchemaVersion", err)
	}
}

func TestVersionRegistryRenderPropagatesRendererError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	reg := render.NewVersionRegistry()
	reg.Register("v1", func(_ []byte) (*renderv1.RenderTree, error) {
		return nil, wantErr
	})

	_, err := reg.Render("v1", nil)
	if !errors.Is(err, wantErr) {
		t.Errorf("Render(v1) error = %v, want wrapping %v", err, wantErr)
	}
}

func TestVersionRegistryRegisterOverwrites(t *testing.T) {
	t.Parallel()

	reg := render.NewVersionRegistry()
	reg.Register("v1", func(_ []byte) (*renderv1.RenderTree, error) {
		return render.Tree(render.Text("first")), nil
	})
	reg.Register("v1", func(_ []byte) (*renderv1.RenderTree, error) {
		return render.Tree(render.Text("second")), nil
	})

	got, err := reg.Render("v1", nil)
	if err != nil {
		t.Fatalf("Render(v1): %v", err)
	}
	if content := got.GetRoot().GetText().GetContent(); content != "second" {
		t.Errorf("Render(v1) content = %q, want %q (last Register wins)", content, "second")
	}
}

func TestVersionRegistryConcurrentRegisterAndRender(t *testing.T) {
	t.Parallel()

	reg := render.NewVersionRegistry()
	const n = 20
	done := make(chan struct{}, n*2)

	for i := range n {
		version := fmt.Sprintf("v%d", i)
		go func() {
			reg.Register(version, func(_ []byte) (*renderv1.RenderTree, error) {
				return render.Tree(render.Text(version)), nil
			})
			done <- struct{}{}
		}()
		go func() {
			_, _ = reg.Render(version, nil) //nolint:errcheck // exercising concurrent access, not asserting outcome
			done <- struct{}{}
		}()
	}

	for range n * 2 {
		<-done
	}
}
