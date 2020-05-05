package rules

import (
	"testing"

	"github.com/influxdata/influxdb/models"
	"github.com/influxdata/influxdb/tsdb/engine/tsm1"
	"github.com/influxdata/influxql"

	"github.com/naoina/toml"
	"github.com/oktal/infix/filter"
	"github.com/stretchr/testify/assert"
)

func TestRenameField_ShouldBuildFromSample(t *testing.T) {
	config := &RenameFieldRuleConfig{}

	table, err := toml.Parse([]byte(config.Sample()))
	assert.NoError(t, err)
	assert.NotNil(t, table)

	err = filter.UnmarshalConfig(table, config)
	assert.NoError(t, err)

	rule, err := config.Build()
	assert.NoError(t, err)
	assert.NotNil(t, rule)
}

func TestRenameField_ShouldBuildFail(t *testing.T) {
	config1 := &RenameFieldRuleConfig{}
	config2 := &RenameFieldRuleConfig{}
	config3 := &RenameFieldRuleConfig{}

	check := func(config *RenameFieldRuleConfig) {
		r, err := config.Build()
		assert.Error(t, err)
		assert.Nil(t, r)
	}

	config1.Measurement = nil
	config1.Field = nil

	config2.Measurement = filter.NewIncludeFilter([]string{"cpu"})
	config2.Field = nil

	config3.Measurement = nil
	config3.Field = filter.NewIncludeFilter([]string{"idle_avg"})

	check(config1)
	check(config2)
	check(config3)
}

func TestRenameField_ShouldApplyAndRename(t *testing.T) {
	measurementFilter := filter.NewIncludeFilter([]string{"cpu"})
	fieldFilter, err := filter.NewPatternFilter("^(.+)_(avg|sum)$")

	assert.NoError(t, err)

	rule := NewRenameField(measurementFilter, fieldFilter, func(value string) string {
		return string(fieldFilter.Pattern.ReplaceAllString(value, "agg_5m_${1}_${2}"))
	})

	key := func(serie string, field string) []byte {
		return tsm1.SeriesFieldKeyBytes(serie, field)
	}

	var data = []struct {
		key    []byte
		values []tsm1.Value

		expectedKey []byte
	}{
		{key("cpu,host=my-host", "idle_avg"), []tsm1.Value{tsm1.NewFloatValue(0, 3.5)}, key("cpu,host=my-host", "agg_5m_idle_avg")},
		{key("cpu,host=my-host", "idle_sum"), []tsm1.Value{tsm1.NewFloatValue(0, 3.5)}, key("cpu,host=my-host", "agg_5m_idle_sum")},
		{key("cpu,host=my-host", "idle_mean"), []tsm1.Value{tsm1.NewFloatValue(0, 3.5)}, key("cpu,host=my-host", "idle_mean")},
		{key("cpu,host=my-host", "active"), []tsm1.Value{tsm1.NewFloatValue(0, 3.5)}, key("cpu,host=my-host", "active")},
		{key("mem,host=my-host", "used_avg"), []tsm1.Value{tsm1.NewFloatValue(0, 3.5)}, key("mem,host=my-host", "used_avg")},
	}

	for _, d := range data {
		key, values, err := rule.Apply(d.key, d.values)

		assert.NoError(t, err)
		assert.Equalf(t, key, d.expectedKey, "expected key '%s' but got '%s'", d.expectedKey, d.key)
		assert.Equal(t, values, d.values)
	}
}

func TestRenameField_ShouldUpdateFieldsIndex(t *testing.T) {
	measurementFilter := filter.NewIncludeFilter([]string{"cpu"})
	fieldFilter, err := filter.NewPatternFilter("^(.+)_(avg|sum)$")

	assert.NoError(t, err)

	rule := NewRenameField(measurementFilter, fieldFilter, func(value string) string {
		return string(fieldFilter.Pattern.ReplaceAllString(value, "agg_5m_${1}_${2}"))
	})

	key := func(serie string, field string) []byte {
		return tsm1.SeriesFieldKeyBytes(serie, field)
	}

	measurements := []measurementFields{
		{
			measurement: "cpu",
			fields: map[string]influxql.DataType{
				"idle_avg":   influxql.Integer,
				"idle_sum":   influxql.Integer,
				"active_avg": influxql.Float,
			},
		},
	}

	shard := newTestShard(measurements)

	var data = []struct {
		key    []byte
		values []tsm1.Value

		newKey []byte
	}{
		{key("cpu,host=my-host", "idle_avg"), []tsm1.Value{tsm1.NewFloatValue(0, 3.5)}, key("cpu,host=my-host", "agg_5m_idle_avg")},
		{key("cpu,host=my-host", "idle_sum"), []tsm1.Value{tsm1.NewFloatValue(0, 3.5)}, key("cpu,host=my-host", "agg_5m_idle_sum")},
		{key("cpu,host=my-host", "active_avg"), []tsm1.Value{tsm1.NewFloatValue(0, 3.5)}, key("cpu,host=my-host", "agg_5m_active_avg")},
	}

	rule.StartShard(shard)

	for _, d := range data {
		_, _, err := rule.Apply(d.key, d.values)
		assert.NoError(t, err)
	}

	err = rule.EndShard()
	assert.NoError(t, err)

	for _, d := range data {
		_, oldFieldKey := tsm1.SeriesAndFieldFromCompositeKey(d.key)
		_, newFieldKey := tsm1.SeriesAndFieldFromCompositeKey(d.newKey)

		measurement, _ := models.ParseKey(d.newKey)
		fields := shard.FieldsIndex.FieldsByString(measurement)

		assert.NotNil(t, fields)

		oldField := fields.FieldBytes(oldFieldKey)
		newField := fields.FieldBytes(newFieldKey)

		assert.Nil(t, oldField)
		assert.NotNil(t, newField)

		assert.Equal(t, newField.Name, string(newFieldKey))
	}
}
