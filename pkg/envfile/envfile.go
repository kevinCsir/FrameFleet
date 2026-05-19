package envfile

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

func Load(paths ...string) error {
	for _, path := range paths {
		if path == "" {
			continue
		}
		if err := loadFile(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
	}
	return nil
}

func LoadWithOverride(overrideEnvKey string, defaults ...string) error {
	paths := append([]string{}, defaults...)
	if override := os.Getenv(overrideEnvKey); override != "" {
		paths = append(paths, override)
	}
	return Load(paths...)
}

func loadFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		if err := parseLine(scanner.Text()); err != nil {
			return fmt.Errorf("%s:%d: %w", path, lineNumber, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	return nil
}

func parseLine(line string) error {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return nil
	}
	line = strings.TrimSpace(strings.TrimPrefix(line, "export "))

	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return fmt.Errorf("expected KEY=value")
	}

	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("empty key")
	}

	if _, exists := os.LookupEnv(key); exists {
		return nil
	}

	return os.Setenv(key, parseValue(value))
}

func parseValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		first := value[0]
		last := value[len(value)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return value[1 : len(value)-1]
		}
	}

	if index := strings.Index(value, " #"); index >= 0 {
		value = strings.TrimSpace(value[:index])
	}
	return value
}
