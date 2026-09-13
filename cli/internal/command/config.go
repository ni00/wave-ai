package command

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	wave "github.com/ni00/wave-ai/sdks/go"
	"github.com/spf13/cobra"
)

type profile struct {
	URL string `json:"url"`
	Key string `json:"api_key,omitempty"`
}
type profileStore struct {
	Version  int                `json:"version"`
	Active   string             `json:"active,omitempty"`
	Profiles map[string]profile `json:"profiles"`
}
type connection struct{ Profile, URL, Key, URLSource, KeySource string }

var profileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func configPath() (string, error) {
	if path := os.Getenv("WAVE_CONFIG_FILE"); path != "" {
		return path, nil
	}
	dir, err := os.UserConfigDir()
	return filepath.Join(dir, "wave-ai", "config.json"), err
}
func loadStore() (*profileStore, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return &profileStore{Version: 1, Profiles: map[string]profile{}}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("profile file exceeds 1 MiB: %s", path)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("invalid profile file %s: %w", path, err)
	}
	if fields == nil {
		return nil, fmt.Errorf("profile file must contain an object: %s", path)
	}
	store := &profileStore{Version: 1, Profiles: map[string]profile{}}
	if raw, ok := fields["version"]; ok && json.Unmarshal(raw, &store.Version) == nil {
		if store.Version != 1 {
			return nil, fmt.Errorf("unsupported profile format version %d", store.Version)
		}
		if err = json.Unmarshal(data, store); err != nil {
			return nil, err
		}
	} else if err = json.Unmarshal(data, &store.Profiles); err != nil {
		return nil, err
	} // v0.1 flat profiles migrate on the next write.
	if store.Profiles == nil {
		store.Profiles = map[string]profile{}
	}
	return store, nil
}
func saveStore(store *profileStore) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err = file.Write(append(data, '\n')); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
func (s *settings) profileFor(store *profileStore) string {
	if s.profile != "" {
		return s.profile
	}
	if name := os.Getenv("WAVE_PROFILE"); name != "" {
		return name
	}
	if store.Active != "" {
		return store.Active
	}
	return "default"
}
func (s *settings) connection() (connection, error) {
	store, err := loadStore()
	if err != nil {
		return connection{}, err
	}
	name := s.profileFor(store)
	p, exists := store.Profiles[name]
	explicit := s.profile != "" || os.Getenv("WAVE_PROFILE") != "" || store.Active != ""
	if !exists && explicit {
		return connection{}, usage("profile %q does not exist; create it with config set %s --url URL", name, name)
	}
	result := connection{Profile: name, URL: p.URL, Key: p.Key, URLSource: "profile", KeySource: "profile"}
	if result.URL == "" {
		result.URL = "http://localhost:8080"
		result.URLSource = "default"
	}
	if v := os.Getenv("WAVE_BASE_URL"); v != "" {
		result.URL = v
		result.URLSource = "environment"
	}
	if s.baseURL != "" {
		result.URL = s.baseURL
		result.URLSource = "flag"
	}
	if key := os.Getenv("WAVE_API_KEY"); key != "" {
		result.Key = key
		result.KeySource = "environment"
	}
	if result.Key == "" {
		result.KeySource = "none"
	}
	if _, err := wave.New(result.URL, result.Key); err != nil {
		return connection{}, usage("%v", err)
	}
	return result, nil
}
func (s *settings) client() (*wave.Client, error) {
	c, err := s.connection()
	if err != nil {
		return nil, err
	}
	return wave.New(c.URL, c.Key)
}
func publicConnection(c connection) map[string]any {
	return map[string]any{"profile": c.Profile, "url": c.URL, "url_source": c.URLSource, "key_source": c.KeySource, "has_key": c.Key != ""}
}
func selectedName(s *settings, store *profileStore, args []string) (string, error) {
	name := s.profileFor(store)
	if len(args) > 0 {
		if s.profile != "" && s.profile != args[0] {
			return "", usage("profile argument conflicts with --profile")
		}
		name = args[0]
	}
	if !profileName.MatchString(name) {
		return "", usage("profile names must use 1–64 letters, digits, dots, underscores or hyphens")
	}
	return name, nil
}

func configCommand(s *settings) *cobra.Command {
	root := &cobra.Command{Use: "config", Short: "Manage named connections without exposing saved keys",
		Example: "  wavectl config set local --url http://localhost:8080\n  wavectl config set local --key-stdin < wave-key.txt\n  wavectl config use local\n  wavectl config show --effective"}
	root.AddCommand(configSetCommand(s), configShowCommand(s), configListCommand(s))
	for _, action := range []string{"use", "remove", "unset-key"} {
		root.AddCommand(configModifyCommand(s, action))
	}
	return root
}
func configSetCommand(s *settings) *cobra.Command {
	var keyStdin bool
	cmd := &cobra.Command{Use: "set [NAME]", Short: "Save a URL and/or a key read from stdin", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if s.baseURL == "" && !keyStdin {
			return usage("provide --url and/or --key-stdin")
		}
		store, err := loadStore()
		if err != nil {
			return err
		}
		name, err := selectedName(s, store, args)
		if err != nil {
			return err
		}
		p := store.Profiles[name]
		if s.baseURL != "" {
			if _, err = wave.New(s.baseURL, ""); err != nil {
				return usage("%v", err)
			}
			p.URL = s.baseURL
		}
		if keyStdin {
			key, err := readKey(cmd.InOrStdin())
			if err != nil {
				return err
			}
			p.Key = key
		}
		store.Profiles[name] = p
		if err = saveStore(store); err != nil {
			return err
		}
		return emit(cmd, map[string]any{"profile": name, "url": p.URL, "has_key": p.Key != "", "active": store.Active == name})
	}}
	cmd.Flags().BoolVar(&keyStdin, "key-stdin", false, "read a Wave key from stdin; save with mode 0600")
	return cmd
}
func readKey(reader io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(reader, 8193))
	if err != nil {
		return "", err
	}
	if len(data) > 8192 {
		return "", usage("key exceeds 8 KiB")
	}
	key := strings.TrimSpace(string(data))
	if strings.HasPrefix(strings.ToLower(key), "bearer ") {
		key = strings.TrimSpace(key[7:])
	}
	if key == "" || strings.ContainsAny(key, "\r\n") {
		return "", usage("provide a single nonempty Wave API key")
	}
	return key, nil
}
func configShowCommand(s *settings) *cobra.Command {
	var effective bool
	cmd := &cobra.Command{Use: "show [NAME]", Short: "Show saved connection metadata; never display keys", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		store, err := loadStore()
		if err != nil {
			return err
		}
		name, err := selectedName(s, store, args)
		if err != nil {
			return err
		}
		if effective {
			copy := *s
			if len(args) > 0 {
				copy.profile = name
			}
			c, err := copy.connection()
			if err != nil {
				return err
			}
			return emit(cmd, publicConnection(c))
		}
		p, exists := store.Profiles[name]
		if !exists {
			return usage("profile %q does not exist", name)
		}
		return emit(cmd, map[string]any{"profile": name, "url": p.URL, "has_key": p.Key != "", "active": store.Active == name})
	}}
	cmd.Flags().BoolVar(&effective, "effective", false, "show resolved URL/key source after flag and environment overrides")
	return cmd
}
func configListCommand(s *settings) *cobra.Command {
	return &cobra.Command{Use: "list", Short: "List saved profile names, URLs and active selection", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		store, err := loadStore()
		if err != nil {
			return err
		}
		names := make([]string, 0, len(store.Profiles))
		for name := range store.Profiles {
			names = append(names, name)
		}
		sort.Strings(names)
		rows := []map[string]any{}
		for _, name := range names {
			p := store.Profiles[name]
			rows = append(rows, map[string]any{"name": name, "url": p.URL, "has_key": p.Key != "", "active": name == s.profileFor(store)})
		}
		return emit(cmd, rows)
	}}
}
func configModifyCommand(s *settings, action string) *cobra.Command {
	descriptions := map[string]string{"use": "Select the default saved profile", "remove": "Remove one saved profile", "unset-key": "Remove a saved API key without changing its URL"}
	return &cobra.Command{Use: action + " NAME", Short: descriptions[action], Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		store, err := loadStore()
		if err != nil {
			return err
		}
		name, err := selectedName(s, store, args)
		if err != nil {
			return err
		}
		p, exists := store.Profiles[name]
		if !exists {
			return usage("profile %q does not exist", name)
		}
		switch action {
		case "use":
			store.Active = name
		case "remove":
			delete(store.Profiles, name)
			if store.Active == name {
				store.Active = ""
			}
		case "unset-key":
			p.Key = ""
			store.Profiles[name] = p
		}
		if err = saveStore(store); err != nil {
			return err
		}
		return emit(cmd, map[string]any{"profile": name, "operation": action, "saved": true})
	}}
}
