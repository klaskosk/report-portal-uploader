package xunit

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Node is one element in a parsed xUnit XML tree.
type Node struct {
	Name     xml.Name
	Attrs    []xml.Attr
	Children []*Node
	Chars    string
	Skip     bool
}

// ParseDocument parses xUnit XML bytes into a mutable tree.
func ParseDocument(input []byte) (*Node, error) {
	decoder := xml.NewDecoder(bytes.NewReader(input))

	var (
		root  *Node
		stack []*Node
	)

	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, fmt.Errorf("parsing XML: %w", err)
		}

		root, stack = appendToken(root, stack, token)
	}

	if root == nil {
		return nil, errors.New("empty XML document")
	}

	return root, nil
}

func appendToken(root *Node, stack []*Node, token xml.Token) (*Node, []*Node) {
	switch element := token.(type) {
	case xml.StartElement:
		node := &Node{Name: element.Name, Attrs: append([]xml.Attr{}, element.Attr...)}
		if len(stack) == 0 {
			root = node
		} else {
			parent := stack[len(stack)-1]
			parent.Children = append(parent.Children, node)
		}

		return root, append(stack, node)
	case xml.EndElement:
		if len(stack) > 0 {
			stack = stack[:len(stack)-1]
		}
	case xml.CharData:
		if len(stack) > 0 {
			stack[len(stack)-1].Chars += string(element)
		}
	}

	return root, stack
}

// EncodeDocument serializes a parsed xUnit tree back to XML bytes.
func EncodeDocument(root *Node) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(xml.Header)

	encoder := xml.NewEncoder(&buf)
	encoder.Indent("", "  ")

	err := writeNode(encoder, root)
	if err != nil {
		return nil, err
	}

	err = encoder.Flush()
	if err != nil {
		return nil, fmt.Errorf("flushing XML encoder: %w", err)
	}

	return buf.Bytes(), nil
}

func writeNode(encoder *xml.Encoder, node *Node) error {
	if node.Skip {
		return nil
	}

	start := xml.StartElement{Name: node.Name, Attr: node.Attrs}

	err := encoder.EncodeToken(start)
	if err != nil {
		return fmt.Errorf("encoding XML start element: %w", err)
	}

	chars := strings.TrimSpace(node.Chars)
	if chars != "" {
		err = encoder.EncodeToken(xml.CharData(chars))
		if err != nil {
			return fmt.Errorf("encoding XML character data: %w", err)
		}
	}

	for _, child := range node.Children {
		err = writeNode(encoder, child)
		if err != nil {
			return err
		}
	}

	err = encoder.EncodeToken(start.End())
	if err != nil {
		return fmt.Errorf("encoding XML end element: %w", err)
	}

	return nil
}

// AttrValue returns the value of a local-name attribute on attrs.
func AttrValue(attrs []xml.Attr, name string) string {
	for _, attr := range attrs {
		if attr.Name.Local == name {
			return attr.Value
		}
	}

	return ""
}

// SetAttr sets or replaces a local-name attribute on node.
func SetAttr(node *Node, name, value string) {
	for i, attr := range node.Attrs {
		if attr.Name.Local == name {
			node.Attrs[i].Value = value

			return
		}
	}

	node.Attrs = append(node.Attrs, xml.Attr{Name: xml.Name{Local: name}, Value: value})
}

// RemoveAttr returns attrs without the named local-name attribute.
func RemoveAttr(attrs []xml.Attr, name string) []xml.Attr {
	out := make([]xml.Attr, 0, len(attrs))
	for _, attr := range attrs {
		if attr.Name.Local != name {
			out = append(out, attr)
		}
	}

	return out
}
