package controllers

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestControllerInit verifies the reconciler can be initialized
func TestControllerInit(t *testing.T) {
	reconciler := &CloudTaskReconciler{}
	require.NotNil(t, reconciler)
}
