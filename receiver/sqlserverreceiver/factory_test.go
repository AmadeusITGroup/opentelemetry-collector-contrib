// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package sqlserverreceiver

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"go.opentelemetry.io/collector/receiver/scraperhelper"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/sqlserverreceiver/internal/metadata"
)

func TestCreateMetricsReceiver(t *testing.T) {
	testCases := []struct {
		desc     string
		testFunc func(*testing.T)
	}{
		{
			desc: "creates a new factory with correct type",
			testFunc: func(t *testing.T) {
				factory := NewFactory()
				require.EqualValues(t, metadata.Type, factory.Type())
			},
		},
		{
			desc: "creates a new factory with valid default config",
			testFunc: func(t *testing.T) {
				factory := NewFactory()

				var expectedCfg component.Config = &Config{
					ControllerConfig: scraperhelper.ControllerConfig{
						CollectionInterval: 10 * time.Second,
						InitialDelay:       time.Second,
					},
					MetricsBuilderConfig: metadata.DefaultMetricsBuilderConfig(),
				}

				require.Equal(t, expectedCfg, factory.CreateDefaultConfig())
			},
		},
		{
			desc: "creates a new factory and CreateMetricsReceiver returns error with incorrect config",
			testFunc: func(t *testing.T) {
				factory := NewFactory()
				_, err := factory.CreateMetricsReceiver(
					context.Background(),
					receivertest.NewNopCreateSettings(),
					nil,
					consumertest.NewNop(),
				)
				require.ErrorIs(t, err, errConfigNotSQLServer)
			},
		},
		{
			desc: "creates a new factory and CreateMetricsReceiver returns no error",
			testFunc: func(t *testing.T) {
				factory := NewFactory()
				cfg := factory.CreateDefaultConfig()
				r, err := factory.CreateMetricsReceiver(
					context.Background(),
					receivertest.NewNopCreateSettings(),
					cfg,
					consumertest.NewNop(),
				)
				require.NoError(t, err)
				scrapers := setupSQLServerScrapers(receivertest.NewNopCreateSettings(), cfg.(*Config))
				require.Empty(t, scrapers)
				require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
				require.NoError(t, r.Shutdown(context.Background()))
			},
		},
		{
			desc: "Test direct connection",
			testFunc: func(t *testing.T) {
				factory := NewFactory()
				cfg := factory.CreateDefaultConfig().(*Config)
				cfg.Username = "sa"
				cfg.Password = "password"
				cfg.Server = "0.0.0.0"
				cfg.Port = 1433
				require.NoError(t, cfg.Validate())

				require.True(t, directDBConnectionEnabled(cfg))
				require.Equal(t, "server=0.0.0.0;user id=sa;password=password;port=1433", getDBConnectionString(cfg))

				params := receivertest.NewNopCreateSettings()
				scrapers, err := setupScrapers(params, cfg)
				require.NoError(t, err)
				require.NotEmpty(t, scrapers)

				sqlScrapers := setupSQLServerScrapers(params, cfg)
				require.NotEmpty(t, sqlScrapers)

				databaseIOScraperFound := false
				for _, scraper := range sqlScrapers {
					if scraper.sqlQuery == getSQLServerDatabaseIOQuery(cfg.InstanceName) {
						databaseIOScraperFound = true
						break
					}
				}

				require.True(t, databaseIOScraperFound)
				cfg.InstanceName = "instanceName"
				sqlScrapers = setupSQLServerScrapers(params, cfg)
				require.NotEmpty(t, sqlScrapers)

				databaseIOScraperFound = false
				for _, scraper := range sqlScrapers {
					if scraper.sqlQuery == getSQLServerDatabaseIOQuery(cfg.InstanceName) {
						databaseIOScraperFound = true
						break
					}
				}

				require.True(t, databaseIOScraperFound)

				r, err := factory.CreateMetricsReceiver(
					context.Background(),
					receivertest.NewNopCreateSettings(),
					cfg,
					consumertest.NewNop(),
				)
				require.NoError(t, err)
				require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
				require.NoError(t, r.Shutdown(context.Background()))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, tc.testFunc)
	}
}
