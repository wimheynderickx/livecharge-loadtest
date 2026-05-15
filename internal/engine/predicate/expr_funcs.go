package predicate

import (
	"fmt"
	"os"
	"strconv"
)

// toInt converts any value to an int. Strings parse as base-10; non-numeric
// strings (including "") return 0. Floats truncate toward zero. nil → 0.
// Matches the spec's "permissive — zero on failure" rule.
func toInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case float32:
		return int(x)
	case string:
		n, err := strconv.Atoi(x)
		if err != nil {
			return 0
		}
		return n
	case nil:
		return 0
	}
	return 0
}

// toFloat converts any value to a float64. Same permissive rule as toInt.
func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case string:
		f, err := strconv.ParseFloat(x, 64)
		if err != nil {
			return 0
		}
		return f
	case nil:
		return 0
	}
	return 0
}

// toString converts any value to its canonical string form. nil → "".
func toString(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	}
	return fmt.Sprintf("%v", v)
}

// envFunc reads an environment variable. Unset → "".
// Named envFunc internally so it doesn't shadow os.Setenv-style tests;
// registered to expr as the user-facing name "env".
func envFunc(name string) string {
	return os.Getenv(name)
}
