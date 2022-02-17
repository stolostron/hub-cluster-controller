package main

import (
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/component-base/cli"
	"k8s.io/component-base/logs"
	_ "k8s.io/component-base/logs/json/register"

	hubcontroller "github.com/stolostron/hub-cluster-controller/pkg"
	"github.com/stolostron/hub-cluster-controller/pkg/version"
)

// The registration binary contains both the hub-side controllers for the
// registration API and the spoke agent.

func main() {
	rand.Seed(time.Now().UTC().UnixNano())

	logsOptions := logs.NewOptions()
	command := newCommand(logsOptions)
	logsOptions.AddFlags(command.Flags())
	code := cli.Run(command)
	os.Exit(code)

	if err := command.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func newCommand(logsOptions *logs.Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "Hub Cluster Deploy",
		Short: "Hub Cluster Deploy",
		Run: func(cmd *cobra.Command, args []string) {
			if err := logsOptions.ValidateAndApply(); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
			}
			os.Exit(1)
		},
	}

	if v := version.Get().String(); len(v) == 0 {
		cmd.Version = "<unknown>"
	} else {
		cmd.Version = v
	}

	cmd.AddCommand(hubcontroller.NewController())

	return cmd
}
