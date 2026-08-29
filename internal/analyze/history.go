package analyze

import (
	"strings"

	"github.com/ericsuh/layerlens/internal/domain"
)

// Decorations that builders add to config history `created_by` strings
// (DECISIONS A5).
const (
	// nopPrefix is what the classic builder writes for instructions that
	// change metadata but still produce (possibly empty) layers, e.g.
	// "/bin/sh -c #(nop)  COPY dir:abc in /app ".
	nopPrefix = "/bin/sh -c #(nop)"
	// shellPrefix is what the classic builder writes for RUN.
	shellPrefix = "/bin/sh -c "
	// buildkitSuffix marks BuildKit-produced history entries, e.g.
	// "RUN /bin/sh -c npm install # buildkit".
	buildkitSuffix = "# buildkit"
	// runShellPrefix is the BuildKit RUN form, which keeps the instruction
	// verb and wraps only the command.
	runShellPrefix = "RUN " + shellPrefix
)

// MapHistory maps the image config's history onto the rootfs.diff_ids array
// (ARCHITECTURE §4.0). history is oldest-first; nLayers is len(diff_ids).
//
// It returns the raw created_by string for each layer index. When the number
// of non-empty history entries does not equal nLayers — squashed, hand-built
// or otherwise unusual images — it returns ok == false and no strings at all.
// Callers then mark every layer InstructionKnown=false: a misaligned guess is
// worse than an honest "unknown", because it would attribute one layer's bytes
// to another layer's instruction.
func MapHistory(history []domain.HistoryEntry, nLayers int) (rawByLayer []string, ok bool) {
	if nLayers < 0 {
		return nil, false
	}
	raw := make([]string, nLayers)
	cursor := 0
	for _, h := range history {
		if h.EmptyLayer {
			continue
		}
		if cursor == nLayers {
			// More non-empty history entries than layers.
			return nil, false
		}
		raw[cursor] = h.CreatedBy
		cursor++
	}
	if cursor != nLayers {
		// Fewer non-empty history entries than layers.
		return nil, false
	}
	return raw, true
}

// CleanInstruction strips builder decorations from a raw created_by string for
// display. The raw string is always kept alongside it in
// domain.Layer.InstructionRaw so the tooltip can show what the config really
// said.
func CleanInstruction(raw string) string {
	s := strings.TrimSpace(raw)

	// BuildKit appends "# buildkit"; strip it before looking at prefixes so
	// that "RUN /bin/sh -c npm install # buildkit" reduces cleanly.
	if trimmed, found := strings.CutSuffix(s, buildkitSuffix); found {
		s = strings.TrimRight(trimmed, " \t")
	}

	switch {
	case strings.HasPrefix(s, nopPrefix):
		// Classic builder metadata instruction: "#(nop)" is followed by
		// the real instruction, sometimes with doubled spaces.
		s = strings.TrimSpace(strings.TrimPrefix(s, nopPrefix))
	case strings.HasPrefix(s, runShellPrefix):
		// BuildKit RUN: keep the verb, drop the shell wrapper.
		s = "RUN " + strings.TrimPrefix(s, runShellPrefix)
	case strings.HasPrefix(s, shellPrefix):
		// Classic builder RUN: the config records only the shell
		// invocation, so the verb is not ours to invent.
		s = strings.TrimPrefix(s, shellPrefix)
	}
	return strings.TrimSpace(s)
}

// ApplyHistory fills the instruction fields of layers from an image config's
// history. It is the one place that decides whether instructions are known at
// all, so no caller can accidentally publish a misaligned mapping.
func ApplyHistory(layers []domain.Layer, history []domain.HistoryEntry) {
	raw, ok := MapHistory(history, len(layers))
	if !ok {
		for i := range layers {
			layers[i].Instruction = ""
			layers[i].InstructionRaw = ""
			layers[i].InstructionKnown = false
		}
		return
	}
	for i := range layers {
		layers[i].InstructionRaw = raw[i]
		layers[i].Instruction = CleanInstruction(raw[i])
		layers[i].InstructionKnown = true
	}
}
