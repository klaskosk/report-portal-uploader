package xunit

import "encoding/xml"

const xmlProperty = "property"

// EnsureProperty sets or updates a <property> child on node.
func EnsureProperty(node *Node, name, value string) {
	properties := FindOrCreateProperties(node)
	for _, child := range properties.Children {
		if child != nil && !child.Skip && child.Name.Local == xmlProperty {
			if AttrValue(child.Attrs, "name") == name {
				SetAttr(child, "value", value)

				return
			}
		}
	}

	properties.Children = append(properties.Children, &Node{
		Name: xml.Name{Local: xmlProperty},
		Attrs: []xml.Attr{
			{Name: xml.Name{Local: "name"}, Value: name},
			{Name: xml.Name{Local: "value"}, Value: value},
		},
	})
}

// FindOrCreateProperties returns the <properties> child of node, creating one if needed.
func FindOrCreateProperties(node *Node) *Node {
	for _, child := range node.Children {
		if child != nil && !child.Skip && child.Name.Local == "properties" {
			return child
		}
	}

	properties := &Node{Name: xml.Name{Local: "properties"}}
	node.Children = append(node.Children, properties)

	return properties
}

// ElementPath returns a descriptive element path for warnings.
func ElementPath(node *Node) string {
	if node == nil {
		return ""
	}

	name := AttrValue(node.Attrs, "name")
	if name == "" {
		return node.Name.Local
	}

	return node.Name.Local + "[@name=" + name + "]"
}
