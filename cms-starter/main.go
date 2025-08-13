/*
 * Firecracker CMS - Starter CLI Tool
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 *
 * This software is proprietary and confidential.
 * Unauthorized copying, distribution, or use is strictly prohibited.
 * See LICENSE file for terms and conditions.
 *
 * Contributors: @centraunit-dev, @issa-projects
 */

package main

import (
	"os"

	"github.com/centraunit/cu-firecracker-cms-starter/cmd"
	"github.com/centraunit/cu-firecracker-cms-starter/internal/logger"
)

func main() {
	if err := cmd.Execute(); err != nil {
		// Log error if logger is available, otherwise print to stderr
		if log := logger.GetDefault(); log != nil {
			log.WithFields(logger.Fields{
				"error": err,
			}).Fatal("Application failed")
		} else {
			os.Exit(1)
		}
	}
}
