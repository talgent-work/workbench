package domain

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A starter's labels are kept as given, ordered by key.
func TestWorkLabelsKeepTheStartersPairs(t *testing.T) {
	labels, err := NewWorkLabels(map[string]string{"user": "u123", "tenant": "acme", "team/region-1.a_b": "Zürich Ost"})
	require.NoError(t, err)
	require.Equal(t, []WorkLabel{{"team/region-1.a_b", "Zürich Ost"}, {"tenant", "acme"}, {"user", "u123"}}, labels.Pairs())
	require.Equal(t, map[string]string{"user": "u123", "tenant": "acme", "team/region-1.a_b": "Zürich Ost"}, labels.Map())

	none, err := NewWorkLabels(nil)
	require.NoError(t, err)
	require.True(t, none.Empty())
	require.Nil(t, none.Map())

	maxed := make(map[string]string, MaxWorkLabels)
	for i := range MaxWorkLabels {
		maxed[fmt.Sprintf("k%d", i)] = "v"
	}
	_, err = NewWorkLabels(maxed)
	require.NoError(t, err)
	_, err = NewWorkLabels(map[string]string{strings.Repeat("k", MaxWorkLabelKeyLength): strings.Repeat("é", MaxWorkLabelValueLength)})
	require.NoError(t, err, "limits are inclusive, and a value counts characters")
}

func TestWorkLabelsRefuseMalformedPairs(t *testing.T) {
	tooMany := make(map[string]string, MaxWorkLabels+1)
	for i := range MaxWorkLabels + 1 {
		tooMany[fmt.Sprintf("k%d", i)] = "v"
	}
	for name, pairs := range map[string]map[string]string{
		"too many pairs":        tooMany,
		"empty key":             {"": "v"},
		"key too long":          {strings.Repeat("k", MaxWorkLabelKeyLength+1): "v"},
		"key starts with a dot": {".user": "v"},
		"key with a space":      {"end user": "v"},
		"key with an equals":    {"user=x": "v"},
		"non-ASCII key":         {"usér": "v"},
		"empty value":           {"user": ""},
		"value too long":        {"user": strings.Repeat("v", MaxWorkLabelValueLength+1)},
		"value on two lines":    {"user": "u1\nu2"},
		"padded value":          {"user": " u1"},
		"invalid UTF-8 value":   {"user": "\xff"},
	} {
		_, err := NewWorkLabels(pairs)
		require.ErrorIs(t, err, ErrInvalidWorkLabels, name)
	}
}

// A Work keeps the labels it was created with (Labels fixed).
func TestAWorkKeepsTheLabelsItWasCreatedWith(t *testing.T) {
	labels, err := NewWorkLabels(map[string]string{"tenant": "acme"})
	require.NoError(t, err)
	work := &AgentWork{WorkLabels: labels}
	launch := work.WorkLabels.Map()
	launch["tenant"] = "other"
	require.Equal(t, map[string]string{"tenant": "acme"}, work.WorkLabels.Map(), "a copy handed out changes nothing")
	pairs := work.WorkLabels.Pairs()
	pairs[0].Value = "other"
	require.True(t, work.WorkLabels.Equal(labels))
}
