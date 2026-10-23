package desktop

import "os"

func EnvironmentLanguage() string {
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return "en"
}
