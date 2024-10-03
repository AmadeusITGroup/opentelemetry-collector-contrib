// Code generated by mdatagen. DO NOT EDIT.

package ecsobserver

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
)

func TestComponentFactoryType(t *testing.T) {
	require.Equal(t, "ecs_observer", NewFactory().Type().String())
}

func TestComponentConfigStruct(t *testing.T) {
	require.NoError(t, componenttest.CheckConfigStruct(NewFactory().CreateDefaultConfig()))
}
