package bench

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"wave-ai.local/wave/internal/adapters/sandbox"
)

func verifyContainer(ctx context.Context, p sandbox.Provider, sid string, checks *[]string) error {
	run := func(id, command string) error {
		res, err := p.Exec(ctx, sid, sandbox.ExecRequest{RequestID: sid + "-verify-" + id, Command: command, TimeoutSec: 30})
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("%s failed (%d): %s %s", id, res.ExitCode, res.Stdout, res.Stderr)
		}
		*checks = append(*checks, id)
		return nil
	}
	if err := run("python-bash", "python3 -c 'print(6*7)' && test -z \"${WAVE_DATABASE_URL:-}\" && test ! -S /var/run/docker.sock && test ! -S /run/podman/podman.sock"); err != nil {
		return err
	}
	if err := p.StageInput(ctx, sid, "test/input.txt", []byte("input")); err != nil {
		return err
	}
	if err := p.StageSkill(ctx, sid, "test", map[string][]byte{"SKILL.md": []byte("skill")}); err != nil {
		return err
	}
	if err := run("read-only-volumes", `test "$(cat /mnt/session/uploads/test/input.txt)" = input && test "$(cat /mnt/skills/test/SKILL.md)" = skill && ! bash -c 'echo changed > /mnt/session/uploads/test/input.txt' && ! rm /mnt/skills/test/SKILL.md && ! mount -o remount,rw /mnt/session/uploads`); err != nil {
		return err
	}
	if err := p.WriteFile(ctx, sid, "/mnt/session/outputs/test/result.txt", []byte("artifact")); err != nil {
		return err
	}
	count := 0
	if err := p.VisitOutputs(ctx, sid, 1024, func(name string, data []byte) error {
		if name != "/mnt/session/outputs/test/result.txt" || string(data) != "artifact" {
			return errors.New("unexpected artifact")
		}
		count++
		return nil
	}); err != nil {
		return err
	}
	if count != 1 {
		return errors.New("missing artifact")
	}
	*checks = append(*checks, "output-publication")
	req := sandbox.ExecRequest{RequestID: sid + "-dedup", Command: "echo x >> /workspace/dedup; cat /workspace/dedup", TimeoutSec: 30}
	a, err := p.Exec(ctx, sid, req)
	if err != nil {
		return err
	}
	b, err := p.Exec(ctx, sid, req)
	if err != nil {
		return err
	}
	if *a != *b {
		return errors.New("exec replay changed result")
	}
	data, err := p.ReadFile(ctx, sid, "/workspace/dedup", 1024)
	if err != nil {
		return err
	}
	if string(data) != "x\n" {
		return errors.New("exec ran twice")
	}
	*checks = append(*checks, "request-id-deduplication")
	res, err := p.Exec(ctx, sid, sandbox.ExecRequest{RequestID: sid + "-capture", Command: "head -c 1000000 /dev/zero", TimeoutSec: 30})
	if err != nil {
		return err
	}
	if len(res.Stdout) > 300000 || !strings.Contains(res.Stdout, "truncated") {
		return errors.New("command capture not bounded")
	}
	*checks = append(*checks, "bounded-output")
	start := time.Now()
	res, err = p.Exec(ctx, sid, sandbox.ExecRequest{RequestID: sid + "-timeout", Command: "(sleep 5; echo leaked > /workspace/leaked) & wait", TimeoutSec: 1})
	if err == nil && res.ExitCode == 0 {
		return errors.New("timeout command unexpectedly succeeded")
	}
	if time.Since(start) > 20*time.Second {
		return errors.New("timeout cleanup exceeded bound")
	}
	if err = p.Ensure(ctx, sid); err != nil {
		return err
	}
	if err = run("timeout-no-surviving-child", "sleep 5; test ! -e /workspace/leaked"); err != nil {
		return err
	}
	return nil
}
