package main

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFlags(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
		want    config
	}{
		{
			name: "defaults match ARCHITECTURE 1.3",
			args: nil,
			want: config{
				listen:        ":8080",
				dataDir:       "/var/lib/layerlens/images",
				cacheMaxBytes: 50 << 30,
				fixturesDir:   "fixtures",
			},
		},
		{
			name: "all flags override",
			args: []string{
				"--listen", "127.0.0.1:9999",
				"--data-dir", "/tmp/ll",
				"--cache-max-bytes", "1048576",
				"--fixtures-dir", "./fx",
				"--docker-host", "unix:///run/docker.sock",
				"--ui-dir", "internal/webui/dist",
			},
			want: config{
				listen:        "127.0.0.1:9999",
				dataDir:       "/tmp/ll",
				cacheMaxBytes: 1 << 20,
				fixturesDir:   "./fx",
				dockerHost:    "unix:///run/docker.sock",
				uiDir:         "internal/webui/dist",
			},
		},
		{
			name:    "non-positive cache cap is rejected",
			args:    []string{"--cache-max-bytes", "0"},
			wantErr: "--cache-max-bytes must be positive",
		},
		{
			name:    "positional arguments are rejected",
			args:    []string{"serve"},
			wantErr: "unexpected arguments",
		},
		{
			name:    "unknown flag is rejected",
			args:    []string{"--nope"},
			wantErr: "flag provided but not defined",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DOCKER_HOST", "")
			got, err := parseFlags(tc.args, io.Discard)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, *got)
		})
	}
}

func TestParseFlagsDefaultsDockerHostFromEnv(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:2375")
	got, err := parseFlags(nil, io.Discard)
	require.NoError(t, err)
	assert.Equal(t, "tcp://127.0.0.1:2375", got.dockerHost)
}
