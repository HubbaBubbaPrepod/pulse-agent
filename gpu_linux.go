package main

import (
	"context"
	"time"
)

type gpuPlatformState struct{}

func (c *gpuCollector) closePlatformGPU() {}

func (c *gpuCollector) collectPlatformGPU(ctx context.Context) gpuReading {
	return c.collectDRM(ctx, "/sys/class/drm", "/proc", time.Now())
}
