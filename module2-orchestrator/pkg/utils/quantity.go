package utils

import (
	"k8s.io/apimachinery/pkg/api/resource"
)

// ParseQuantity parses a string into a Kubernetes Quantity
func ParseQuantity(s string) resource.Quantity {
	q, err := resource.ParseQuantity(s)
	if err != nil {
		// Return a default value if parsing fails
		return resource.Quantity{}
	}
	return q
}

// FormatQuantity formats a Quantity to a string
func FormatQuantity(q resource.Quantity) string {
	return q.String()
}
