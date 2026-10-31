// hoofprint is the CLI: a thin client over the HTTP API.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sarkar-ai-taken/hoofprint/internal/model"
	"github.com/sarkar-ai-taken/hoofprint/internal/version"
)

const usage = `hoofprint — the deploy gate for code nobody read

Usage:
  hoofprint [--api URL] <command> [args]

Commands:
  agents                       list agents and their status
  workloads <agent-id> [ns]    ask an agent to list its workloads
  deploys [--namespace ns]     list observed deploys, newest first
  deploy <id>                  show one deploy with its verdicts
  verdicts [--deploy id]       list verdicts
  explain <verdict-id>         show a verdict with its evidence
  version                      print client and server versions

Environment:
  HOOFPRINT_API   API base URL (default http://localhost:8080)
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type client struct {
	base string
	http *http.Client
}

func run(args []string) error {
	fs := flag.NewFlagSet("hoofprint", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	api := fs.String("api", envOr("HOOFPRINT_API", "http://localhost:8080"), "API base URL")
	asJSON := fs.Bool("json", false, "print raw JSON")
	if err := fs.Parse(args); err != nil {
		fmt.Fprint(os.Stderr, usage)
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("command required")
	}
	c := &client{base: strings.TrimRight(*api, "/"), http: &http.Client{Timeout: 90 * time.Second}}
	cmd, rest := rest[0], rest[1:]
	switch cmd {
	case "agents":
		var out struct{ Agents []*model.Agent }
		if err := c.get("/api/v1/agents", nil, &out, *asJSON); err != nil {
			return err
		}
		if *asJSON {
			return nil
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tCLUSTER\tSTATUS\tVERSION\tLAST HEARTBEAT\tCAPABILITIES\tTAGS")
		for _, a := range out.Agents {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", a.ID, a.Cluster, a.Status, a.Version, ago(a.LastHeartbeat), strings.Join(a.Capabilities, ","), tags(a.Tags))
		}
		return tw.Flush()
	case "workloads":
		if len(rest) < 1 {
			return fmt.Errorf("usage: hoofprint workloads <agent-id> [namespace]")
		}
		ns := ""
		if len(rest) > 1 {
			ns = rest[1]
		}
		body, _ := json.Marshal(map[string]any{"kind": "workloads.list", "payload": map[string]string{"namespace": ns}})
		var res struct {
			OK      bool            `json:"ok"`
			Error   string          `json:"error"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := c.post("/api/v1/agents/"+url.PathEscape(rest[0])+"/work", body, &res, *asJSON); err != nil {
			return err
		}
		if *asJSON {
			return nil
		}
		if !res.OK {
			return fmt.Errorf("agent error: %s", res.Error)
		}
		var out struct {
			Workloads []struct {
				Namespace, Kind, Name, Revision string
				Replicas, Ready                 int32
				Images                          []string
			}
		}
		if err := json.Unmarshal(res.Payload, &out); err != nil {
			return err
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "NAMESPACE\tKIND\tNAME\tREADY\tREVISION\tIMAGES")
		for _, w := range out.Workloads {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d/%d\t%s\t%s\n", w.Namespace, w.Kind, w.Name, w.Ready, w.Replicas, w.Revision, strings.Join(w.Images, ","))
		}
		return tw.Flush()
	case "deploys":
		sub := flag.NewFlagSet("deploys", flag.ContinueOnError)
		ns := sub.String("namespace", "", "filter by namespace")
		cluster := sub.String("cluster", "", "filter by cluster")
		limit := sub.Int("limit", 50, "max rows")
		if err := sub.Parse(rest); err != nil {
			return err
		}
		q := url.Values{"namespace": {*ns}, "cluster": {*cluster}, "limit": {fmt.Sprint(*limit)}}
		var out struct{ Deploys []*model.Deploy }
		if err := c.get("/api/v1/deploys", q, &out, *asJSON); err != nil {
			return err
		}
		if *asJSON {
			return nil
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tOBSERVED\tCLUSTER\tNAMESPACE\tKIND\tNAME\tTIER\tREVISION\tIMAGE")
		for _, d := range out.Deploys {
			img := ""
			if len(d.ImagesAfter) > 0 {
				img = d.ImagesAfter[0].Image
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", short(d.ID), ago(d.ObservedAt), d.Cluster, d.Namespace, d.Kind, d.Name, d.Tier, rev(d), img)
		}
		return tw.Flush()
	case "deploy":
		if len(rest) != 1 {
			return fmt.Errorf("usage: hoofprint deploy <id>")
		}
		var out struct {
			model.Deploy
			Verdicts []*model.Verdict `json:"verdicts"`
		}
		if err := c.get("/api/v1/deploys/"+url.PathEscape(rest[0]), nil, &out, *asJSON); err != nil {
			return err
		}
		if *asJSON {
			return nil
		}
		fmt.Printf("Deploy %s\n  %s/%s %s in %s (tier: %s", out.ID, out.Namespace, out.Name, out.Kind, out.Cluster, out.Tier)
		if out.Controller != "" {
			fmt.Printf(", controller: %s", out.Controller)
		}
		fmt.Printf(")\n  observed %s by agent %s\n  revision %s -> %s\n", out.ObservedAt.Format(time.RFC3339), out.AgentID, orDash(out.RevisionBefore), orDash(out.RevisionAfter))
		for i, img := range out.ImagesAfter {
			before := ""
			if i < len(out.ImagesBefore) {
				before = out.ImagesBefore[i].Image
			}
			fmt.Printf("  image %s: %s -> %s\n", img.Container, orDash(before), img.Image)
		}
		if len(out.Verdicts) == 0 {
			fmt.Println("  verdicts: none yet")
		}
		for _, v := range out.Verdicts {
			fmt.Printf("  verdict %s [%s] %s (%.0f%%): %s\n", short(v.ID), v.Judge, strings.ToUpper(string(v.Outcome)), v.Confidence*100, v.Rationale)
		}
		return nil
	case "verdicts":
		sub := flag.NewFlagSet("verdicts", flag.ContinueOnError)
		dep := sub.String("deploy", "", "filter by deploy id")
		if err := sub.Parse(rest); err != nil {
			return err
		}
		var out struct{ Verdicts []*model.Verdict }
		if err := c.get("/api/v1/verdicts", url.Values{"deploy_id": {*dep}}, &out, *asJSON); err != nil {
			return err
		}
		if *asJSON {
			return nil
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tCREATED\tDEPLOY\tJUDGE\tOUTCOME\tCONFIDENCE\tRATIONALE")
		for _, v := range out.Verdicts {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%.0f%%\t%s\n", short(v.ID), ago(v.CreatedAt), short(v.DeployID), v.Judge, v.Outcome, v.Confidence*100, truncate(v.Rationale, 80))
		}
		return tw.Flush()
	case "explain":
		if len(rest) != 1 {
			return fmt.Errorf("usage: hoofprint explain <verdict-id>")
		}
		var out struct {
			model.Verdict
			Evidence []*model.Evidence `json:"evidence"`
		}
		if err := c.get("/api/v1/verdicts/"+url.PathEscape(rest[0]), nil, &out, *asJSON); err != nil {
			return err
		}
		if *asJSON {
			return nil
		}
		fmt.Printf("Verdict %s on deploy %s\n  %s (confidence %.0f%%) by %s at %s\n\n  %s\n\nEvidence (%d):\n", out.ID, out.DeployID, strings.ToUpper(string(out.Outcome)), out.Confidence*100, out.Judge, out.CreatedAt.Format(time.RFC3339), out.Rationale, len(out.Evidence))
		for _, e := range out.Evidence {
			printEvidence(e)
		}
		return nil
	case "version":
		fmt.Printf("client: %s\n", version.Version)
		var out struct{ Version string }
		if err := c.get("/api/v1/version", nil, &out, false); err != nil {
			fmt.Printf("server: unreachable (%v)\n", err)
			return nil
		}
		fmt.Printf("server: %s\n", out.Version)
		return nil
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

// metricEvidence mirrors verify.metricEvidence's summary fields.
type metricEvidence struct {
	Name           string      `json:"name"`
	Outcome        string      `json:"outcome"`
	Direction      string      `json:"direction"`
	Critical       bool        `json:"critical"`
	BaselineMedian float64     `json:"baseline_median"`
	CanaryMedian   float64     `json:"canary_median"`
	PctChange      float64     `json:"pct_change"`
	PValue         float64     `json:"p_value"`
	CliffsDelta    float64     `json:"cliffs_delta"`
	BaselineN      int         `json:"baseline_n"`
	CanaryN        int         `json:"canary_n"`
	Query          string      `json:"query"`
	BaselineWindow []time.Time `json:"baseline_window"`
	PostWindow     []time.Time `json:"post_window"`
}

func printEvidence(e *model.Evidence) {
	switch e.Kind {
	case "metric":
		var m metricEvidence
		if json.Unmarshal(e.Payload, &m) != nil {
			break
		}
		crit := ""
		if m.Critical {
			crit = ", critical"
		}
		fmt.Printf("  [metric] %-24s %-6s %s -> %s (%+.0f%%)  p=%.3g  delta=%+.2f  n=%d/%d  (%s is better%s)\n",
			m.Name, strings.ToUpper(m.Outcome), fmtNum(m.BaselineMedian), fmtNum(m.CanaryMedian), m.PctChange, m.PValue, m.CliffsDelta, m.BaselineN, m.CanaryN, m.Direction, crit)
		if len(m.BaselineWindow) == 2 && len(m.PostWindow) == 2 {
			fmt.Printf("           before %s..%s   after %s..%s\n", m.BaselineWindow[0].Local().Format("15:04:05"), m.BaselineWindow[1].Local().Format("15:04:05"), m.PostWindow[0].Local().Format("15:04:05"), m.PostWindow[1].Local().Format("15:04:05"))
		}
		if m.Query != "" {
			fmt.Printf("           %s\n", strings.Join(strings.Fields(m.Query), " "))
		}
		return
	case "change":
		fmt.Printf("  [change] %s\n", e.Title)
		return
	}
	fmt.Printf("  [%s] %s\n", e.Kind, e.Title)
	var pretty bytes.Buffer
	if json.Indent(&pretty, e.Payload, "    ", "  ") == nil {
		fmt.Printf("    %s\n", pretty.String())
	} else {
		fmt.Printf("    %s\n", string(e.Payload))
	}
}

func fmtNum(f float64) string {
	switch {
	case f == 0:
		return "0"
	case f < 0.01:
		return fmt.Sprintf("%.2g", f)
	case f < 10:
		return fmt.Sprintf("%.3g", f)
	default:
		return fmt.Sprintf("%.0f", f)
	}
}

func (c *client) get(path string, q url.Values, out any, raw bool) error {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	resp, err := c.http.Get(u)
	if err != nil {
		return err
	}
	return c.decode(resp, out, raw)
}

func (c *client) post(path string, body []byte, out any, raw bool) error {
	resp, err := c.http.Post(c.base+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	return c.decode(resp, out, raw)
}

func (c *client) decode(resp *http.Response, out any, raw bool) error {
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var e struct{ Error string }
		_ = json.Unmarshal(b, &e)
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(b))
		}
		return fmt.Errorf("%s: %s", resp.Status, e.Error)
	}
	if raw {
		var pretty bytes.Buffer
		if json.Indent(&pretty, b, "", "  ") == nil {
			b = pretty.Bytes()
		}
		fmt.Println(string(b))
	}
	return json.Unmarshal(b, out)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t).Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return t.Format("2006-01-02")
	}
}

func short(id string) string {
	if len(id) > 8 && strings.Count(id, "-") == 4 {
		return id[:8]
	}
	return id
}

func rev(d *model.Deploy) string {
	if d.RevisionBefore == "" && d.RevisionAfter == "" {
		return "-"
	}
	return fmt.Sprintf("%s..%s", shortRev(d.RevisionBefore), shortRev(d.RevisionAfter))
}

func shortRev(r string) string {
	if r == "" {
		return "?"
	}
	if len(r) > 7 {
		return r[:7]
	}
	return r
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func tags(m map[string]string) string {
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ",")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
