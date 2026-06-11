package send

// Warning records a non-fatal pipeline issue with an optional source stage tag.
type Warning struct {
	Source  string `json:"source,omitempty"`
	Message string `json:"message"`
}

// WarningList aggregates warnings from pipeline stages.
type WarningList []Warning

// Add appends a warning tagged with source.
func (warnings *WarningList) Add(source, message string) {
	*warnings = append(*warnings, Warning{Source: source, Message: message})
}

// Messages returns plain warning strings for JSON output.
func (warnings *WarningList) Messages() []string {
	out := make([]string, len(*warnings))
	for i, warning := range *warnings {
		out[i] = warning.Message
	}

	return out
}
