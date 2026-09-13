package sandbox

import (
	"context"

	"fmt"

	"time"

	"connectrpc.com/connect"

	processv1 "github.com/docker/sandboxes-api/gen/go/docker/sbx/process/v1"
)

// Exec runs a durable process with a stable per-call request id so create
// retries deduplicate on the backend, and collects output until exit.
func (s *Sbx) Exec(ctx context.Context, sessionID string, req ExecRequest) (*ExecResult, error) {
	sc, err := s.client(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	cwd := req.Cwd
	if cwd == "" {
		cwd = "/workspace"
	}
	requestID := req.RequestID
	if requestID == "" {
		requestID = fmt.Sprintf("exec-%s-%d", sessionID, time.Now().UnixNano())
	}
	proc, err := sc.Processes().CreateProcess(ctx, connect.NewRequest(&processv1.CreateProcessRequest{
		Cmd:        []string{"bash", "-c", req.Command},
		WorkingDir: cwd,
		User:       "root",
		RequestId:  requestID,
		Session:    sessionID,
	}))
	if err != nil {
		return nil, fmt.Errorf("sbx create process: %w", err)
	}
	stream := sc.Processes().Interact(ctx)
	if err := stream.Send(&processv1.InteractRequest{
		Input: &processv1.InteractRequest_Attach{Attach: &processv1.Attach{
			ProcessId: proc.Msg.ProcessId, ResumeFrom: 0,
		}},
	}); err != nil {
		return nil, fmt.Errorf("sbx attach: %w", err)
	}
	_ = stream.CloseRequest()
	res := &ExecResult{}
	for {
		out, err := stream.Receive()
		if err != nil {
			if res.ExitCode != 0 {
				return res, nil
			}
			return res, fmt.Errorf("sbx stream: %w", err)
		}
		switch f := out.Frame.(type) {
		case *processv1.Output_Chunk:
			if f.Chunk.Stream == processv1.StreamType_STREAM_TYPE_STDOUT {
				res.Stdout = boundedAppend(res.Stdout, string(f.Chunk.Data))
			} else {
				res.Stderr = boundedAppend(res.Stderr, string(f.Chunk.Data))
			}
		case *processv1.Output_Exited:
			res.ExitCode = int(f.Exited.ExitCode)
			return res, nil
		}
	}
}

// boundedAppend caps the in-memory capture of one output stream so a
// runaway process cannot exhaust worker memory.
func boundedAppend(buf, chunk string) string {
	if len(buf) >= maxCaptureBytes {
		return buf
	}
	if len(buf)+len(chunk) < maxCaptureBytes {
		return buf + chunk
	}
	return buf + chunk[:maxCaptureBytes-len(buf)-1] + "\n…[output truncated]"
}
