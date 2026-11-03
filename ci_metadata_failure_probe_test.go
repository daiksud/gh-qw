package main

import "testing"

// Temporary negative CI probe. This file must be removed before merge.
func TestCIMetadataFailureProbe(t *testing.T) {
	t.Fatal("Intentional temporary failure for PR metadata check validation")
}
