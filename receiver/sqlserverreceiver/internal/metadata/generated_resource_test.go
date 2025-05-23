// Code generated by mdatagen. DO NOT EDIT.

package metadata

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResourceBuilder(t *testing.T) {
	for _, tt := range []string{"default", "all_set", "none_set"} {
		t.Run(tt, func(t *testing.T) {
			cfg := loadResourceAttributesConfig(t, tt)
			rb := NewResourceBuilder(cfg)
			rb.SetHostName("host.name-val")
			rb.SetServerAddress("server.address-val")
			rb.SetServerPort(11)
			rb.SetSqlserverComputerName("sqlserver.computer.name-val")
			rb.SetSqlserverDatabaseName("sqlserver.database.name-val")
			rb.SetSqlserverInstanceName("sqlserver.instance.name-val")

			res := rb.Emit()
			assert.Equal(t, 0, rb.Emit().Attributes().Len()) // Second call should return empty Resource

			switch tt {
			case "default":
				assert.Equal(t, 2, res.Attributes().Len())
			case "all_set":
				assert.Equal(t, 6, res.Attributes().Len())
			case "none_set":
				assert.Equal(t, 0, res.Attributes().Len())
				return
			default:
				assert.Failf(t, "unexpected test case: %s", tt)
			}

			val, ok := res.Attributes().Get("host.name")
			assert.True(t, ok)
			if ok {
				assert.Equal(t, "host.name-val", val.Str())
			}
			val, ok = res.Attributes().Get("server.address")
			assert.Equal(t, tt == "all_set", ok)
			if ok {
				assert.Equal(t, "server.address-val", val.Str())
			}
			val, ok = res.Attributes().Get("server.port")
			assert.Equal(t, tt == "all_set", ok)
			if ok {
				assert.EqualValues(t, 11, val.Int())
			}
			val, ok = res.Attributes().Get("sqlserver.computer.name")
			assert.Equal(t, tt == "all_set", ok)
			if ok {
				assert.Equal(t, "sqlserver.computer.name-val", val.Str())
			}
			val, ok = res.Attributes().Get("sqlserver.database.name")
			assert.True(t, ok)
			if ok {
				assert.Equal(t, "sqlserver.database.name-val", val.Str())
			}
			val, ok = res.Attributes().Get("sqlserver.instance.name")
			assert.Equal(t, tt == "all_set", ok)
			if ok {
				assert.Equal(t, "sqlserver.instance.name-val", val.Str())
			}
		})
	}
}
