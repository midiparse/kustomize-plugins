//go:build e2e

package main

import (
	"testing"

	"github.com/midiparse/kustomize-plugins/internal/testutils"
)

func TestResourceInjector(t *testing.T) {
	testutils.TestE2E(t, "./.")
}
