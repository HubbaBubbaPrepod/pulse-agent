//go:build !linux && !windows

package main

import "context"

type gpuPlatformState struct{}

func (c *gpuCollector) closePlatformGPU() {}

func (c *gpuCollector) collectPlatformGPU(ctx context.Context) gpuReading {
	return gpuReading{}
}
