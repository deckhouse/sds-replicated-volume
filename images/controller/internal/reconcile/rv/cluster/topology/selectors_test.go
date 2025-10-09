package topology_test

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/reconcile/rv/cluster/topology"
	"github.com/google/go-cmp/cmp"
)

//go:embed testdata/selectors_tests.txt
var testCasesText []byte

type setNodeArgs struct {
	Node   string
	Zone   string
	Scores []topology.Score
}

type customSelectArgs struct {
	Counts []int
}

type customSelectResult struct {
	ExpectedResult [][]string
	ExpectedError  string
}

type customRun struct {
	Act    customSelectArgs
	Assert customSelectResult
}

// CustomSuite holds one suite with common arrange and multiple runs
type CustomSuite struct {
	Name    string
	Arrange []setNodeArgs
	Runs    []customRun
}

func parseCustomSuites(data []byte) ([]CustomSuite, error) {
	lines := strings.Split(string(data), "\n")

	suites := make([]CustomSuite, 0)
	var cur *CustomSuite
	var pendingAct *customSelectArgs

	flush := func() {
		if cur != nil {
			suites = append(suites, *cur)
			cur = nil
			pendingAct = nil
		}
	}

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if line == "---" {
			flush()
			continue
		}
		if cur == nil {
			cur = &CustomSuite{Name: line}
			continue
		}

		if after, ok := strings.CutPrefix(line, ">"); ok {
			line = strings.TrimSpace(after)
			counts, err := parseCountsCSV(line)
			if err != nil {
				return nil, fmt.Errorf("parse counts: %w", err)
			}
			pendingAct = &customSelectArgs{Counts: counts}
			continue
		}
		if strings.HasPrefix(line, "<") {
			if pendingAct == nil {
				return nil, fmt.Errorf("assert without act in suite %q", cur.Name)
			}
			line = strings.TrimSpace(strings.TrimPrefix(line, "<"))
			var res customSelectResult
			if after, ok := strings.CutPrefix(line, "err="); ok {
				res.ExpectedError = after
			} else {
				groups, err := parseResultGroups(line)
				if err != nil {
					return nil, fmt.Errorf("parse result: %w", err)
				}
				res.ExpectedResult = groups
			}
			cur.Runs = append(cur.Runs, customRun{Act: *pendingAct, Assert: res})
			pendingAct = nil
			continue
		}

		zone, node, scores, err := parseArrangeLine(line)
		if err != nil {
			return nil, fmt.Errorf("parse arrange: %w", err)
		}
		cur.Arrange = append(cur.Arrange, setNodeArgs{Node: node, Zone: zone, Scores: scores})
	}
	flush()
	return suites, nil
}

func parseArrangeLine(line string) (string, string, []topology.Score, error) {
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return "", "", nil, fmt.Errorf("expected name=s1,s2,..., got %q", line)
	}
	name := strings.TrimSpace(parts[0])
	zone := ""
	if before, after, ok := strings.Cut(name, "/"); ok {
		zone = strings.TrimSpace(before)
		name = strings.TrimSpace(after)
	}
	scoresCSV := strings.TrimSpace(parts[1])
	tokens := splitCSV(scoresCSV)
	scores := make([]topology.Score, 0, len(tokens))
	for _, tok := range tokens {
		switch tok {
		case "A":
			scores = append(scores, topology.AlwaysSelect)
		case "N":
			scores = append(scores, topology.NeverSelect)
		default:
			n, err := strconv.ParseInt(tok, 10, 64)
			if err != nil {
				return "", "", nil, fmt.Errorf("invalid score %q: %w", tok, err)
			}
			scores = append(scores, topology.Score(n))
		}
	}
	return zone, name, scores, nil
}

func parseCountsCSV(line string) ([]int, error) {
	toks := splitCSV(line)
	res := make([]int, 0, len(toks))
	for _, t := range toks {
		n, err := strconv.Atoi(t)
		if err != nil {
			return nil, fmt.Errorf("invalid count %q: %w", t, err)
		}
		res = append(res, n)
	}
	return res, nil
}

func parseResultGroups(line string) ([][]string, error) {
	// Example: a,b,(c,d)
	groups := make([][]string, 0)
	i := 0
	for i < len(line) {
		switch line[i] {
		case ',':
			i++
			continue
		case '(':
			j := strings.IndexByte(line[i+1:], ')')
			if j < 0 {
				return nil, fmt.Errorf("missing closing ) in %q", line[i:])
			}
			inner := line[i+1 : i+1+j]
			i += 1 + j + 1
			items := filterNonEmpty(splitCSV(inner))
			groups = append(groups, items)
		default:
			// read token until comma or end
			j := i
			for j < len(line) && line[j] != ',' {
				j++
			}
			tok := strings.TrimSpace(line[i:j])
			if tok != "" {
				groups = append(groups, []string{tok})
			}
			i = j
		}
	}
	return groups, nil
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func filterNonEmpty(s []string) []string {
	out := s[:0]
	for _, v := range s {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func TestSelectors(t *testing.T) {
	suites, err := parseCustomSuites(testCasesText)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	for _, suite := range suites {
		t.Run(suite.Name, func(t *testing.T) {
			if len(suite.Arrange) == 0 {
				t.Fatalf("no arrange entries")
			}
			var nozone, transzonal, zonal bool
			if strings.HasPrefix(suite.Name, "nozone") {
				nozone = true
			} else if strings.HasPrefix(suite.Name, "transzonal") {
				transzonal = true
			} else if strings.HasPrefix(suite.Name, "zonal") {
				zonal = true
			} else {
				// default to nozone for backward compatibility
				nozone = true
			}

			var selectFunc func(counts []int) ([][]string, error)
			if nozone {
				s := topology.NewMultiPurposeNodeSelector(len(suite.Arrange[0].Scores))
				for _, a := range suite.Arrange {
					s.SetNode(a.Node, a.Scores)
				}
				selectFunc = s.SelectNodes
			} else if transzonal {
				s := topology.NewTransZonalMultiPurposeNodeSelector(len(suite.Arrange[0].Scores))
				for _, a := range suite.Arrange {
					s.SetNode(a.Node, a.Zone, a.Scores)
				}
				selectFunc = s.SelectNodes
			} else if zonal {
				s := topology.NewZonalMultiPurposeNodeSelector(len(suite.Arrange[0].Scores))
				for _, a := range suite.Arrange {
					s.SetNode(a.Node, a.Zone, a.Scores)
				}
				selectFunc = s.SelectNodes
			}
			for _, run := range suite.Runs {
				t.Run(fmt.Sprintf("%v", run.Act.Counts), func(t *testing.T) {
					nodes, err := selectFunc(run.Act.Counts)

					if run.Assert.ExpectedError != "" {
						if err == nil {
							t.Fatalf("expected error, got nil")
						} else if !strings.Contains(err.Error(), run.Assert.ExpectedError) {
							t.Fatalf("expected error to contain '%s', got '%s'", run.Assert.ExpectedError, err.Error())
						}
					} else if err != nil {
						t.Fatalf("expected nil error, got %v", err)
					} else if diff := cmp.Diff(run.Assert.ExpectedResult, nodes); diff != "" {
						t.Errorf("mismatch (-want +got):\n%s", diff)
					}
				})
			}
		})
	}
}
