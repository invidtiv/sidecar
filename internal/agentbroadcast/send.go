package agentbroadcast

import (
	"context"
	"strings"
	"sync"

	"github.com/marcus/sidecar/internal/agentcontrol"
)

const sendConcurrency = 4

// Send fans out Control.Prompt to every would_send row. A refusing target
// does not stop the others. There is no path to Start or Launch.
func (s Service) Send(ctx context.Context, plan Plan, text string) (Result, error) {
	if strings.TrimSpace(text) == "" {
		return Result{}, &agentcontrol.Error{Code: agentcontrol.ErrNotReady, Message: "prompt text is required"}
	}
	delivered := text
	if !plan.Raw {
		delivered = Envelope(envelopeSender(plan.FromUser, plan.SenderName, plan.SenderProject), text)
	}

	out := make([]Recipient, len(plan.Recipients))
	copy(out, plan.Recipients)

	sem := make(chan struct{}, sendConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := range out {
		if out[i].Outcome != OutcomeWouldSend {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				mu.Lock()
				out[i].Outcome = OutcomeSkipped
				out[i].Reason = reasonFromErr(ctx.Err())
				mu.Unlock()
				return
			}
			res, err := s.Control.Prompt(ctx, agentcontrol.PromptRequest{
				Target: out[i].Target,
				Text:   delivered,
				Wait:   false,
			})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				applyPromptError(&out[i], err)
				return
			}
			out[i].Outcome = OutcomeSubmitted
			out[i].Agent = res.Agent
			out[i].Target = res.Target
			rec := res.Receipt
			out[i].Receipt = &rec
			out[i].Reason = nil
		}(i)
	}
	wg.Wait()

	return Result{
		Text:       delivered,
		Scope:      plan.Scope,
		Recipients: out,
		Summary:    summarize(out, plan.ShellsWithoutAgent),
	}, nil
}

func applyPromptError(row *Recipient, err error) {
	var typed *agentcontrol.Error
	if !agentcontrol.AsError(err, &typed) {
		row.Outcome = OutcomeUnknown
		row.Reason = &Reason{Code: string(agentcontrol.ErrTransport), Message: err.Error()}
		return
	}
	if typed.Receipt != nil {
		rec := *typed.Receipt
		row.Receipt = &rec
		switch typed.Receipt.Submission {
		case agentcontrol.SubmissionSubmitted:
			row.Outcome = OutcomeSubmitted
			row.Reason = nil
			return
		case agentcontrol.SubmissionUnknown:
			row.Outcome = OutcomeUnknown
			row.Reason = &Reason{Code: string(typed.Code), Message: typed.Message}
			return
		}
	}
	row.Outcome = OutcomeSkipped
	row.Reason = &Reason{Code: string(typed.Code), Message: typed.Message}
}

func summarize(rows []Recipient, shellsWithoutAgent int) Summary {
	sum := Summary{ShellsWithoutAgent: shellsWithoutAgent}
	for _, row := range rows {
		switch row.Outcome {
		case OutcomeSubmitted:
			sum.Submitted++
		case OutcomeUnknown:
			sum.Unknown++
		default:
			sum.Skipped++
		}
	}
	return sum
}
