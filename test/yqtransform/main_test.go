//go:build e2e

package main

import (
	"testing"

	"github.com/midiparse/kustomize-plugins/internal/testutils"
)

func TestYQTransform(t *testing.T) {
	testutils.TestE2E(t, "./.")
}
