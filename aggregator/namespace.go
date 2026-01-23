package aggregator

import (
	"path"
	"sync"

	"github.com/michaelquigley/df/dl"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NamespacedTool represents a tool with its namespace information.
type NamespacedTool struct {
	Tool         mcp.Tool
	BackendID    string
	OriginalName string
}

// Namespace manages the aggregated tool registry with namespacing.
type Namespace struct {
	separator string
	tools     map[string]*NamespacedTool
	mu        sync.RWMutex
}

// NewNamespace creates a new namespace manager.
func NewNamespace(separator string) *Namespace {
	return &Namespace{
		separator: separator,
		tools:     make(map[string]*NamespacedTool),
	}
}

// AddTools adds tools from a backend, applying the filter and namespace prefix.
// this implements structural enforcement: only permitted tools are added.
func (n *Namespace) AddTools(backendID string, tools []*mcp.Tool, filter *ToolFilterConfig) {
	n.mu.Lock()
	defer n.mu.Unlock()

	for _, tool := range tools {
		if tool == nil {
			continue
		}
		if !n.isToolPermitted(tool.Name, filter) {
			dl.Log().With("backend", backendID).With("tool", tool.Name).Debug("tool filtered out by configuration")
			continue
		}

		namespacedName := backendID + n.separator + tool.Name
		namespacedTool := mcp.Tool{
			Name:         namespacedName,
			Description:  tool.Description,
			InputSchema:  tool.InputSchema,
			OutputSchema: tool.OutputSchema,
			Annotations:  tool.Annotations,
		}

		n.tools[namespacedName] = &NamespacedTool{
			Tool:         namespacedTool,
			BackendID:    backendID,
			OriginalName: tool.Name,
		}

		dl.Log().With("backend", backendID).With("tool", tool.Name).With("namespaced", namespacedName).Debug("registered tool")
	}
}

// isToolPermitted checks if a tool passes the filter using glob pattern matching.
func (n *Namespace) isToolPermitted(name string, filter *ToolFilterConfig) bool {
	if filter == nil || len(filter.List) == 0 {
		// no filter means all tools permitted
		return true
	}

	inList := n.matchesPatternList(name, filter.List)

	switch filter.Mode {
	case "allow":
		return inList
	case "deny":
		return !inList
	default:
		// default to allow mode if mode is empty
		if filter.Mode == "" && len(filter.List) > 0 {
			return inList
		}
		return true
	}
}

// matchesPatternList checks if name matches any pattern in the list.
// supports glob patterns using path.Match (e.g., "create_*", "list_*").
func (n *Namespace) matchesPatternList(name string, patterns []string) bool {
	for _, pattern := range patterns {
		matched, err := path.Match(pattern, name)
		if err != nil {
			// invalid pattern, treat as literal match
			if pattern == name {
				return true
			}
			continue
		}
		if matched {
			return true
		}
	}
	return false
}

// GetTool looks up a tool by its namespaced name.
func (n *Namespace) GetTool(namespacedName string) (*NamespacedTool, bool) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	tool, ok := n.tools[namespacedName]
	return tool, ok
}

// AllTools returns all registered tools for tools/list response.
func (n *Namespace) AllTools() []mcp.Tool {
	n.mu.RLock()
	defer n.mu.RUnlock()

	result := make([]mcp.Tool, 0, len(n.tools))
	for _, nt := range n.tools {
		result = append(result, nt.Tool)
	}
	return result
}

// Count returns the number of registered tools.
func (n *Namespace) Count() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.tools)
}
