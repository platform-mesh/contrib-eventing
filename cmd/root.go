/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

// Package cmd has one command per component. They share one binary so there
// is only one image to build and load.
package cmd

import (
	"github.com/spf13/cobra"

	pmconfig "go.platform-mesh.io/golang-commons/config"
	"go.platform-mesh.io/golang-commons/logger"
)

var (
	defaultCfg = pmconfig.NewDefaultConfig()
	log        *logger.Logger
)

var rootCmd = &cobra.Command{
	Use:   "eventing",
	Short: "Eventing for Platform Mesh",
}

func init() {
	defaultCfg.AddFlags(rootCmd.PersistentFlags())
	rootCmd.AddCommand(subscriptionControllerCmd, openFGAAdapterCmd, keycloakAdapterCmd, ingressCmd)
	cobra.OnInitialize(initLog)
}

func initLog() { // coverage-ignore
	logcfg := logger.DefaultConfig()
	logcfg.Name = "contrib-eventing"
	logcfg.Level = defaultCfg.Log.Level
	logcfg.NoJSON = defaultCfg.Log.NoJson

	var err error
	log, err = logger.New(logcfg)
	if err != nil {
		panic(err)
	}
}

func Execute() { // coverage-ignore
	cobra.CheckErr(rootCmd.Execute())
}
