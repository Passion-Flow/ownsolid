package datasets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

// NearPromptV1 groups lexically similar prompts before splitting. It removes
// only direct matches to earlier retained rows with identical completions.
const NearPromptV1 = "near_prompt_v1"

const (
	nearMinimumShingles = 24
	nearIndexCeiling    = 1_000_000
	nearIndexUnitBytes  = 512
	nearVisitCeiling    = 2_000_000
	nearCompareCeiling  = 32_000_000
	nearPlanRowBytes    = 64
	nearBudgetError     = "DATASET_NEAR_DUPLICATE_BUDGET_EXCEEDED"
)

// Group is the first effective input line in a prompt component. Match, when
// set, points directly to an earlier retained line, never to a removed row.
type nearPlanRow struct {
	Group int `json:"group"`
	Match int `json:"match"`
}

type promptShingle [5]rune

func compareShingle(a, b promptShingle) int {
	for i := range a {
		if a[i] != b[i] {
			return int(a[i] - b[i])
		}
	}
	return 0
}

func promptShingles(prompt string) []promptShingle {
	runes := []rune(prompt)
	if len(runes) < len(promptShingle{}) {
		return nil
	}
	result := make([]promptShingle, 0, len(runes)-len(promptShingle{})+1)
	for i := range cap(result) {
		result = append(result, promptShingle(runes[i:i+len(promptShingle{})]))
	}
	slices.SortFunc(result, compareShingle)
	return slices.Clip(slices.Compact(result))
}

func nearPromptSignals(prompt string) string {
	var marks strings.Builder
	for _, r := range prompt {
		if !unicode.IsLetter(r) && !unicode.IsSpace(r) || strings.ContainsRune("\u4e0d\u6ca1\u672a\u65e0\u52ff\u5426\u7981\u975e", r) {
			marks.WriteRune(r)
		}
	}
	for _, word := range strings.FieldsFunc(prompt, func(r rune) bool { return !unicode.IsLetter(r) }) {
		word = strings.ToLower(word)
		switch word {
		case "no", "not", "never", "without", "cannot", "neither", "nor", "none":
			marks.WriteByte(0)
			marks.WriteString(word)
		}
	}
	return marks.String()
}

func nearSimilar(a, b []promptShingle) bool {
	if 10*min(len(a), len(b)) < 9*max(len(a), len(b)) {
		return false
	}
	common := 0
	for i, j := 0, 0; i < len(a) && j < len(b); {
		c := compareShingle(a[i], b[j])
		if c == 0 {
			common++
		}
		if c <= 0 {
			i++
		}
		if c >= 0 {
			j++
		}
	}
	return 19*common >= 9*(len(a)+len(b))
}

type indexedPrompt struct {
	normalized [32]byte
	completion [32]byte
	shingles   []promptShingle
	signals    string
	length     int
}

type nearPlanner struct {
	index                               map[promptShingle][]int
	identical                           map[[32]byte][]int
	exact                               map[string]int
	prompts                             []indexedPrompt
	plan                                []nearPlanRow
	units, visits, comparisons          int
	unitLimit, visitLimit, compareLimit int
}

func newNearPlanner(memoryBytes int64) *nearPlanner {
	return &nearPlanner{
		index:      make(map[promptShingle][]int),
		identical:  make(map[[32]byte][]int),
		exact:      make(map[string]int),
		unitLimit:  min(nearIndexCeiling, int(memoryBytes/nearIndexUnitBytes)),
		visitLimit: nearVisitCeiling, compareLimit: nearCompareCeiling,
	}
}

func (p *nearPlanner) root(line int) int {
	for p.plan[line-1].Group != line {
		parent := p.plan[line-1].Group
		p.plan[line-1].Group = p.plan[parent-1].Group
		line = parent
	}
	return line
}

func (p *nearPlanner) join(a, b int) {
	a, b = p.root(a), p.root(b)
	p.plan[max(a, b)-1].Group = min(a, b)
}

func (p *nearPlanner) add(ctx context.Context, row wire.Record, digest string) error {
	line := len(p.plan) + 1
	p.plan = append(p.plan, nearPlanRow{Group: line})
	p.prompts = append(p.prompts, indexedPrompt{})
	if first := p.exact[digest]; first != 0 {
		p.join(line, first)
		p.plan[line-1].Match = p.plan[first-1].Match
		return nil
	}
	p.exact[digest] = line
	normalized := strings.Join(strings.Fields(row.Prompt), " ")
	prompt := indexedPrompt{
		normalized: sha256.Sum256([]byte(normalized)),
		completion: sha256.Sum256([]byte(row.Completion)),
		shingles:   promptShingles(normalized),
		signals:    nearPromptSignals(normalized),
		length:     utf8.RuneCountInString(normalized),
	}
	size := len(prompt.shingles)
	prefix := prompt.shingles[:0]
	if size >= nearMinimumShingles {
		prefix = prompt.shingles[:size-(9*size+9)/10+1]
	}
	// Charge even repetitive prompts for allocated shingle storage. Account
	// for prompt metadata and every posting before adding it to the index.
	p.units += prompt.length + len(prefix) + 1
	if p.units > p.unitLimit {
		return failure(nearBudgetError)
	}
	visited := make(map[int]struct{})
	candidates := func(lines []int) error {
		p.visits += len(lines)
		if p.visits > p.visitLimit {
			return failure(nearBudgetError)
		}
		for _, prior := range lines {
			visited[prior] = struct{}{}
		}
		return nil
	}
	if e := candidates(p.identical[prompt.normalized]); e != nil {
		return e
	}
	for _, gram := range prefix {
		if e := candidates(p.index[gram]); e != nil {
			return e
		}
	}
	match := 0
	for prior := range visited {
		if e := ctx.Err(); e != nil {
			return e
		}
		old := p.prompts[prior-1]
		comparable := min(size, len(old.shingles)) >= nearMinimumShingles
		if old.normalized != prompt.normalized {
			if !comparable {
				continue
			}
			p.comparisons += size + len(old.shingles)
			if p.comparisons > p.compareLimit {
				return failure(nearBudgetError)
			}
			if !nearSimilar(old.shingles, prompt.shingles) {
				continue
			}
		}
		p.join(line, prior)
		if comparable && 10*min(old.length, prompt.length) >= 9*max(old.length, prompt.length) &&
			p.plan[prior-1].Match == 0 && old.completion == prompt.completion &&
			old.signals == prompt.signals && (match == 0 || prior < match) {
			match = prior
		}
	}
	p.plan[line-1].Match = match
	p.prompts[line-1] = prompt
	p.identical[prompt.normalized] = append(p.identical[prompt.normalized], line)
	for _, gram := range prefix {
		p.index[gram] = append(p.index[gram], line)
	}
	return nil
}

func (s *Store) stageNearPlan(ctx context.Context, request wire.ProcessorRequest, source io.Reader, removeNearDuplicates bool) (string, error) {
	planner := newNearPlanner(request.Resources.MemoryBytes)
	var planError error
	e := scanLines(source, request.Limits.MaximumLineBytes, request.Limits.MaximumRows, func(_ int64, raw []byte) error {
		record, e := wire.DecodeRecord(raw, request.Limits)
		if e != nil {
			return e
		}
		digest, e := record.ContentDigest(request.Limits)
		if e != nil {
			return e
		}
		planError = ctx.Err()
		if planError == nil {
			planError = planner.add(ctx, record, digest)
		}
		return planError
	})
	if planError != nil {
		if errors.Is(planError, context.DeadlineExceeded) {
			return "", failure("DATASET_PROCESSOR_TIMEOUT")
		}
		if errors.Is(planError, context.Canceled) {
			return "", failure("DATASET_JOB_CANCELLED")
		}
		return "", planError
	}
	if e != nil {
		return "", e
	}
	for i := range planner.plan {
		planner.plan[i].Group = planner.root(i + 1)
		if !removeNearDuplicates {
			planner.plan[i].Match = 0
		}
	}
	raw, e := json.Marshal(planner.plan)
	if e != nil {
		return "", e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", failure("DATASET_STORAGE_UNAVAILABLE")
	}
	if e = s.reserve(int64(len(raw))); e != nil {
		return "", e
	}
	if e = writeNew(filepath.Join(request.ScopeDirectory, wire.NearDuplicatePlanFile), raw); e != nil {
		return "", e
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func readNearPlan(request wire.ProcessorRequest) ([]nearPlanRow, error) {
	if request.NearDuplicatePlanSHA256 == "" {
		return nil, nil
	}
	path := filepath.Join(request.ScopeDirectory, wire.NearDuplicatePlanFile)
	maximum := request.Limits.MaximumRows * nearPlanRowBytes
	if _, e := privatePath(path, request.ScopeDirectory, false, maximum); e != nil {
		return nil, e
	}
	file, e := os.Open(path)
	if e != nil {
		return nil, failure("DATASET_FILE_INVALID")
	}
	defer file.Close()
	raw, e := io.ReadAll(io.LimitReader(file, maximum+1))
	if e != nil || int64(len(raw)) > maximum {
		return nil, failure("DATASET_FILE_INVALID")
	}
	digest := sha256.Sum256(raw)
	var plan []nearPlanRow
	if hex.EncodeToString(digest[:]) != request.NearDuplicatePlanSHA256 || wire.DecodeStrict(raw, &plan) != nil ||
		len(plan) == 0 || int64(len(plan)) > request.Limits.MaximumRows {
		return nil, failure("DATASET_FILE_INVALID")
	}
	for i, row := range plan {
		line := i + 1
		if row.Group < 1 || row.Group > line || plan[row.Group-1].Group != row.Group ||
			row.Match < 0 || row.Match >= line ||
			row.Match != 0 && (plan[row.Match-1].Match != 0 || plan[row.Match-1].Group != row.Group) {
			return nil, failure("DATASET_FILE_INVALID")
		}
	}
	return plan, nil
}
