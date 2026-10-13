package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestCollectorHelper(t *testing.T) {
	if os.Getenv("PULSE_TEST_HELPER") != "1" {
		return
	}
	var v map[string]any
	json.NewDecoder(os.Stdin).Decode(&v)
	config, _ := v["config"].(map[string]any)
	if config["hang"] == true {
		time.Sleep(10 * time.Second)
	}
	fmt.Print(`{"count":3}`)
	os.Exit(0)
}
func TestCollectorIsBoundedAndCancelled(t *testing.T) {
	t.Setenv("PULSE_TEST_HELPER", "1")
	got, err := runCollector(context.Background(), os.Args[0], map[string]any{}, "-test.run=TestCollectorHelper")
	if err != nil || string(got) != `{"count":3}` {
		t.Fatal(string(got), err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := runCollector(ctx, os.Args[0], map[string]any{"hang": true}, "-test.run=TestCollectorHelper"); err == nil {
		t.Fatal("hung collector was not cancelled")
	}
}
