package main

import (
	"testing"

	"github.com/midiparse/kustomize-plugins/internal/testutils"
)

func TestVendorKustomization(t *testing.T) {
	testutils.TestE2E(t, "./.")
}
