package xunit_test

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/klaskosk/report-portal-uploader/internal/xunit"
	"github.com/stretchr/testify/require"
)

func TestParseEncode_roundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{
			name: "minimal testsuite",
			input: `<testsuite name="Suite" tests="1">
  <testcase classname="C" name="N" time="0.1"/>
</testsuite>`,
		},
		{
			name: "nested testsuites with properties",
			input: `<testsuites>
  <testsuite name="Outer">
    <properties>
      <property name="lane" value="main"/>
    </properties>
    <testcase name="T">
      <failure message="boom">details</failure>
    </testcase>
  </testsuite>
</testsuites>`,
		},
		{
			name: "character data in system-out",
			input: `<testsuite name="S">
  <testcase name="T">
    <system-out>line one&#xA;line two</system-out>
  </testcase>
</testsuite>`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root, err := xunit.ParseDocument([]byte(test.input))
			require.NoError(t, err, "expected parse to succeed")

			encoded, err := xunit.EncodeDocument(root)
			require.NoError(t, err, "expected encode to succeed")

			roundTrip, err := xunit.ParseDocument(encoded)
			require.NoError(t, err, "expected round-trip parse to succeed")

			assertNodeEqual(t, root, roundTrip)
		})
	}
}

func TestAttrHelpers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		attrs  []xml.Attr
		lookup string
		want   string
	}{
		{
			name:   "finds local name",
			attrs:  []xml.Attr{{Name: xml.Name{Local: "time"}, Value: "1.0"}},
			lookup: "time",
			want:   "1.0",
		},
		{
			name:   "missing attribute",
			attrs:  []xml.Attr{{Name: xml.Name{Local: "name"}, Value: "Suite"}},
			lookup: "time",
			want:   "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := xunit.AttrValue(test.attrs, test.lookup)
			require.Equal(t, test.want, got, "unexpected attribute value")
		})
	}

	node := &xunit.Node{Name: xml.Name{Local: "testcase"}}
	xunit.SetAttr(node, "name", "Example")
	require.Equal(t, "Example", xunit.AttrValue(node.Attrs, "name"), "expected set attribute")

	xunit.SetAttr(node, "name", "Updated")
	require.Equal(t, "Updated", xunit.AttrValue(node.Attrs, "name"), "expected updated attribute")

	xunit.SetAttr(node, "time", "0.5")
	require.Equal(t, "0.5", xunit.AttrValue(node.Attrs, "time"), "expected appended attribute")

	attrs := xunit.RemoveAttr(node.Attrs, "time")
	require.Empty(t, xunit.AttrValue(attrs, "time"), "expected time attribute removed")
	require.Equal(
		t,
		"Updated",
		xunit.AttrValue(attrs, "name"),
		"expected other attributes preserved",
	)
}

func TestEncodeDocument_skipsMarkedNodes(t *testing.T) {
	t.Parallel()

	root := &xunit.Node{
		Name: xml.Name{Local: "testsuite"},
		Attrs: []xml.Attr{
			{Name: xml.Name{Local: "name"}, Value: "Suite"},
		},
		Children: []*xunit.Node{
			{Name: xml.Name{Local: "testcase"}, Skip: true, Attrs: []xml.Attr{
				{Name: xml.Name{Local: "name"}, Value: "removed"},
			}},
			{Name: xml.Name{Local: "testcase"}, Attrs: []xml.Attr{
				{Name: xml.Name{Local: "name"}, Value: "kept"},
			}},
		},
	}

	encoded, err := xunit.EncodeDocument(root)
	require.NoError(t, err, "expected encode to succeed")

	text := string(encoded)
	require.NotContains(t, text, "removed", "expected skipped node omitted")
	require.Contains(t, text, "kept", "expected non-skipped node preserved")
}

func assertNodeEqual(t *testing.T, want, got *xunit.Node) {
	t.Helper()

	require.Equal(t, want.Name, got.Name, "unexpected element name")
	require.Equal(t, want.Chars, got.Chars, "unexpected character data")
	require.Equal(t, want.Skip, got.Skip, "unexpected skip flag")
	require.ElementsMatch(t, want.Attrs, got.Attrs, "unexpected attributes")
	require.Len(t, got.Children, len(want.Children), "unexpected child count")

	for i := range want.Children {
		assertNodeEqual(t, want.Children[i], got.Children[i])
	}
}

func TestElementPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		node *xunit.Node
		want string
	}{
		{
			name: "named element",
			node: &xunit.Node{
				Name:  xml.Name{Local: "testsuite"},
				Attrs: []xml.Attr{{Name: xml.Name{Local: "name"}, Value: "Suite"}},
			},
			want: "testsuite[@name=Suite]",
		},
		{
			name: "unnamed element",
			node: &xunit.Node{Name: xml.Name{Local: "testcase"}},
			want: "testcase",
		},
		{
			name: "nil node",
			node: nil,
			want: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := xunit.ElementPath(test.node)
			require.Equal(t, test.want, got, "unexpected element path")
		})
	}
}

func TestEnsureProperty(t *testing.T) {
	t.Parallel()

	node := &xunit.Node{Name: xml.Name{Local: "testsuite"}}
	xunit.EnsureProperty(node, "lane", "main")

	properties := xunit.FindOrCreateProperties(node)
	require.Len(t, properties.Children, 1, "expected one property child")
	require.Equal(t, "lane", xunit.AttrValue(properties.Children[0].Attrs, "name"))
	require.Equal(t, "main", xunit.AttrValue(properties.Children[0].Attrs, "value"))

	xunit.EnsureProperty(node, "lane", "updated")
	require.Len(t, properties.Children, 1, "expected property updated in place")
	require.Equal(t, "updated", xunit.AttrValue(properties.Children[0].Attrs, "value"))

	xunit.EnsureProperty(node, "group", "gm")
	require.Len(t, properties.Children, 2, "expected second property appended")
}

func TestParseDocument_rejectsEmptyInput(t *testing.T) {
	t.Parallel()

	_, err := xunit.ParseDocument([]byte(strings.TrimSpace("")))
	require.Error(t, err, "expected empty document to fail")
}
