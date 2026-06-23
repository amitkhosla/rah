package steps

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/tidwall/gjson"
)

func TestArrayPathHandling(t *testing.T) {
	data := map[string]interface{}{
		"categories": []map[string]interface{}{
			{
				"name": "Cat1",
				"services": []map[string]string{
					{"code": "svc1", "url": "http://svc1"},
					{"code": "svc2", "url": "http://svc2"},
				},
			},
		},
	}

	jsonBytes, _ := json.Marshal(data)

	// Test: Path to array
	result := gjson.GetBytes(jsonBytes, "categories.0.services")
	fmt.Println("=== When path returns ARRAY ===")
	fmt.Printf("IsArray: %v\n", result.IsArray())
	fmt.Printf("String(): %s\n", result.String())
	fmt.Printf("Array elements: %d\n", len(result.Array()))
	
	for i, elem := range result.Array() {
		fmt.Printf("  [%d] %s\n", i, elem.String())
	}

	// Test: Wildcard syntax
	fmt.Println("\n=== Using #.# wildcard ===")
	result2 := gjson.GetBytes(jsonBytes, "categories.#.services.#.code")
	fmt.Printf("Result: %v\n", result2.Array())
}
