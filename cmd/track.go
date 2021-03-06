// Copyright © 2019 Ken'ichiro Oyama <k1lowxb@gmail.com>
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/antonmedv/expr"
	"github.com/k1LoW/sheer-heart-attack/logger"
	"github.com/k1LoW/sheer-heart-attack/metrics"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var force bool

// trackCmd represents the track command
var trackCmd = &cobra.Command{
	Use:   "track",
	Short: "Track the process metrics and execute command when the threshold is exceeded.",
	Long:  `Track the process metrics and execute command when the threshold is exceeded.`,
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return errors.New("track require no args")
		}
		return nil
	},
	Run: func(cmd *cobra.Command, args []string) {
		if isatty.IsTerminal(os.Stdout.Fd()) && !force {
			_, _ = fmt.Fprintf(os.Stderr, "%s\n", "can not execute `track` directly. execute via `launch`, or use `--force` option")
			os.Exit(1)
		}
		ctx, cancel := context.WithCancel(context.Background())
		timer := time.NewTimer(time.Duration(timeout) * time.Second)
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		envs := os.Environ()
		logPath, err := filepath.Abs(fmt.Sprintf("sheer-heart-attack-%s.log", time.Now().Format(time.RFC3339)))
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "%s\n", err)
			os.Exit(1)
		}
		l := logger.NewLogger(logPath)
		exceeded := 0
		executed := 0
		l.Info("", zap.String("msg", "start tracking"))

	L:
		for {
			select {
			case <-timer.C:
				l.Info("", zap.String("msg", "tracking timeout"))
				cancel()
				break L
			case <-ticker.C:
				m, err := metrics.Get(pid)
				if err != nil {
					l.Error("", zap.Error(err))
					break L
				}
				got, err := expr.Eval(fmt.Sprintf("(%s) == true", threshold), m)
				if err != nil {
					l.Error("", zap.Error(err))
					break L
				}
				if got.(bool) {
					exceeded++
				} else {
					exceeded = 0
				}
				if exceeded >= attempts {
					stdout, stderr, err := execute(ctx, command, envs, interval)
					executed++
					exceeded = 0
					fields := []zap.Field{
						zap.String("msg", "execute command"),
						zap.ByteString("stdout", stdout),
						zap.ByteString("stderr", stderr),
					}
					for k, v := range m {
						fields = append(fields, zap.Any(k, v))
					}
					l.Info("", fields...)
					if err != nil {
						l.Error("", zap.Error(err))
						// do not break
					}
				}
				if times > 0 && executed >= times {
					cancel()
					break L
				}
			case <-ctx.Done():
				cancel()
				break L
			}
		}

		l.Info("", zap.String("msg", "tracking ended"))
	},
}

func init() {
	rootCmd.AddCommand(trackCmd)
	trackCmd.Flags().Int32VarP(&pid, "pid", "", 0, "PID of the process")
	trackCmd.Flags().StringVarP(&threshold, "threshold", "", "cpu > 10", "Threshold conditions")
	trackCmd.Flags().IntVarP(&interval, "interval", "", 5, "Interval of checking if the threshold exceeded (seconds)")
	trackCmd.Flags().IntVarP(&attempts, "attempts", "", 1, "Maximum number of attempts continuously exceeding the threshold")
	trackCmd.Flags().StringVarP(&command, "command", "", "", "Command to execute when the maximum number of attempts is exceeded")
	trackCmd.Flags().IntVarP(&times, "times", "", 1, "Maximum number of command executions. If times < 1, track and execute until timeout")
	trackCmd.Flags().IntVarP(&timeout, "timeout", "", 60*60*24, "Timeout of tracking (seconds)")
	trackCmd.Flags().BoolVarP(&force, "force", "", false, "Force execute 'track' command on tty")
}

func execute(ctx context.Context, command string, envs []string, timeout int) ([]byte, []byte, error) {
	innerCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	c := exec.CommandContext(innerCtx, "sh", "-c", command)
	c.Env = envs
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Start(); err != nil {
		return stdout.Bytes(), stderr.Bytes(), err
	}
	if err := c.Wait(); err != nil {
		return stdout.Bytes(), stderr.Bytes(), err
	}
	return stdout.Bytes(), stderr.Bytes(), nil
}
