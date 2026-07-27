package main

import (
	"testing"

	"github.com/pluggableharness/agent/pkg/model/modeltest"
)

// TestConformance runs the shared conformance suite against this example.
//
// It is the check that makes the example trustworthy as a starting point:
// a reference an author copies from must itself satisfy the requirements
// it is meant to demonstrate. Running from a separate module also proves
// modeltest is reachable and usable by a third party, which is the whole
// premise of shipping it in pkg/.
func TestConformance(t *testing.T) {
	t.Parallel()

	// No WithExpectedIdentity: in-process the identity is modeltest's own,
	// so the expectation is unverifiable there. RunBinary is where a
	// plugin's own identity stamping gets checked.
	modeltest.Run(t, &echoProvider{greeting: "hello"})
}
