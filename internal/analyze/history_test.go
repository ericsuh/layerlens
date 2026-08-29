package analyze_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/analyze"
	"github.com/ericsuh/layerlens/internal/domain"
)

func layerEntry(createdBy string) domain.HistoryEntry {
	return domain.HistoryEntry{CreatedBy: createdBy}
}

func metaEntry(createdBy string) domain.HistoryEntry {
	return domain.HistoryEntry{CreatedBy: createdBy, EmptyLayer: true}
}

func TestMapHistory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		history []domain.HistoryEntry
		nLayers int
		want    []string
		wantOK  bool
	}{
		{
			// The whole point of the empty_layer cursor: metadata
			// instructions interleaved with real ones must not shift
			// the mapping.
			name: "empty_layer_offsets",
			history: []domain.HistoryEntry{
				layerEntry("/bin/sh -c #(nop) ADD file:base in /"),
				metaEntry("/bin/sh -c #(nop)  ENV NODE_VERSION=22"),
				metaEntry("/bin/sh -c #(nop)  CMD [\"node\"]"),
				layerEntry("RUN /bin/sh -c apt-get update # buildkit"),
				metaEntry("WORKDIR /app"),
				layerEntry("COPY . . # buildkit"),
				metaEntry("EXPOSE map[3000/tcp:{}]"),
				layerEntry("RUN /bin/sh -c npm install # buildkit"),
			},
			nLayers: 4,
			want: []string{
				"/bin/sh -c #(nop) ADD file:base in /",
				"RUN /bin/sh -c apt-get update # buildkit",
				"COPY . . # buildkit",
				"RUN /bin/sh -c npm install # buildkit",
			},
			wantOK: true,
		},
		{
			name: "more_history_than_layers",
			history: []domain.HistoryEntry{
				layerEntry("RUN a"), layerEntry("RUN b"), layerEntry("RUN c"),
			},
			nLayers: 2,
			wantOK:  false,
		},
		{
			name: "more_layers_than_history",
			history: []domain.HistoryEntry{
				layerEntry("RUN a"), metaEntry("ENV X=1"),
			},
			nLayers: 3,
			wantOK:  false,
		},
		{
			name:    "no_history_at_all",
			history: nil,
			nLayers: 2,
			wantOK:  false,
		},
		{
			name:    "no_history_no_layers",
			history: nil,
			nLayers: 0,
			want:    []string{},
			wantOK:  true,
		},
		{
			name:    "all_empty_layers_no_layers",
			history: []domain.HistoryEntry{metaEntry("ENV X=1"), metaEntry("CMD [\"sh\"]")},
			nLayers: 0,
			want:    []string{},
			wantOK:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := analyze.MapHistory(tc.history, tc.nLayers)
			require.Equal(t, tc.wantOK, ok)
			if !tc.wantOK {
				assert.Nil(t, got, "a failed mapping must return nothing, never a guess")
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestCleanInstruction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "buildkit_cleaning",
			raw:  "RUN /bin/sh -c npm install # buildkit",
			want: "RUN npm install",
		},
		{
			name: "buildkit_copy",
			raw:  "COPY . . # buildkit",
			want: "COPY . .",
		},
		{
			name: "buildkit_heredoc_run",
			raw:  "RUN /bin/sh -c apt-get update && apt-get install -y ffmpeg # buildkit",
			want: "RUN apt-get update && apt-get install -y ffmpeg",
		},
		{
			name: "classic_nop_cleaning",
			raw:  "/bin/sh -c #(nop) COPY dir:0f2 in /app ",
			want: "COPY dir:0f2 in /app",
		},
		{
			name: "classic_nop_double_space",
			raw:  "/bin/sh -c #(nop)  CMD [\"node\" \"server.js\"]",
			want: "CMD [\"node\" \"server.js\"]",
		},
		{
			name: "classic_run",
			raw:  "/bin/sh -c npm install",
			want: "npm install",
		},
		{
			name: "already_clean",
			raw:  "WORKDIR /app",
			want: "WORKDIR /app",
		},
		{
			name: "empty",
			raw:  "",
			want: "",
		},
		{
			// "# buildkit" only ever appears as a suffix; a hash
			// inside a shell command must survive.
			name: "hash_inside_command_preserved",
			raw:  "RUN /bin/sh -c echo '# buildkit is not a suffix here' > /f # buildkit",
			want: "RUN echo '# buildkit is not a suffix here' > /f",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, analyze.CleanInstruction(tc.raw))
		})
	}
}

func TestApplyHistory(t *testing.T) {
	t.Parallel()

	t.Run("known", func(t *testing.T) {
		t.Parallel()
		layers := make([]domain.Layer, 2)
		analyze.ApplyHistory(layers, []domain.HistoryEntry{
			layerEntry("/bin/sh -c #(nop) ADD file:base in /"),
			metaEntry("ENV PATH=/usr/bin"),
			layerEntry("RUN /bin/sh -c npm install # buildkit"),
		})
		assert.Equal(t, "ADD file:base in /", layers[0].Instruction)
		assert.Equal(t, "/bin/sh -c #(nop) ADD file:base in /", layers[0].InstructionRaw)
		assert.True(t, layers[0].InstructionKnown)
		assert.Equal(t, "RUN npm install", layers[1].Instruction)
		assert.True(t, layers[1].InstructionKnown)
	})

	t.Run("count_mismatch_marks_all_unknown", func(t *testing.T) {
		t.Parallel()
		layers := []domain.Layer{
			{Instruction: "stale", InstructionRaw: "stale", InstructionKnown: true},
			{Instruction: "stale", InstructionRaw: "stale", InstructionKnown: true},
			{Instruction: "stale", InstructionRaw: "stale", InstructionKnown: true},
		}
		analyze.ApplyHistory(layers, []domain.HistoryEntry{layerEntry("RUN a")})
		for i, l := range layers {
			assert.False(t, l.InstructionKnown, "layer %d", i)
			assert.Empty(t, l.Instruction, "layer %d", i)
			assert.Empty(t, l.InstructionRaw, "layer %d", i)
		}
	})
}
