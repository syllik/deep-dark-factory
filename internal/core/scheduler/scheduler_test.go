package scheduler_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/scheduler"
)

var base = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

// task returns a task with key k in state s that wants a one-container run,
// created base plus age minutes; opts adjust it.
func task(k string, s model.TaskState, age int, opts ...func(*scheduler.Task)) scheduler.Task {
	t := scheduler.Task{
		Key:        model.TaskKey(k),
		State:      s,
		CreatedAt:  base.Add(time.Duration(age) * time.Minute),
		WantsRun:   true,
		Containers: 1,
	}
	for _, o := range opts {
		o(&t)
	}
	return t
}

func zones(z ...string) func(*scheduler.Task) { return func(t *scheduler.Task) { t.Zones = z } }

func deps(d ...string) func(*scheduler.Task) {
	return func(t *scheduler.Task) {
		for _, k := range d {
			t.Dependencies = append(t.Dependencies, model.TaskKey(k))
		}
	}
}

func prio(p scheduler.Priority) func(*scheduler.Task) {
	return func(t *scheduler.Task) { t.Priority = p }
}

func followup(t *scheduler.Task) { t.Kind = scheduler.KindFollowup }

func idle(t *scheduler.Task) { t.WantsRun = false; t.Containers = 0 }

func flag(f model.TaskFlag) func(*scheduler.Task) {
	return func(t *scheduler.Task) { t.Flags = append(t.Flags, f) }
}

func containers(n int) func(*scheduler.Task) { return func(t *scheduler.Task) { t.Containers = n } }

func capOf(n int) *int { return &n }

func admitted(d scheduler.Decision) []string {
	out := []string{}
	for _, a := range d.Admit {
		out = append(out, string(a.Key))
	}
	return out
}

func keys(ts []scheduler.Transition) []string {
	out := []string{}
	for _, t := range ts {
		out = append(out, string(t.Key))
	}
	return out
}

func zoneReason(z string) scheduler.Reason {
	return scheduler.Reason{Kind: scheduler.ReasonZone, Zone: z}
}

func depReason(k string) scheduler.Reason {
	return scheduler.Reason{Kind: scheduler.ReasonDependency, Dependency: model.TaskKey(k)}
}

func TestDecide(t *testing.T) {
	tests := []struct {
		name      string
		in        scheduler.Input
		admit     []string
		toWaiting []scheduler.Transition
		toReady   []string
	}{
		{
			name: "zone held by running", in: scheduler.Input{Tasks: []scheduler.Task{
				task("h", model.TaskStateRunning, 0, idle, zones("z")),
				task("r", model.TaskStateReady, 1, zones("z")),
			}},
			admit:     []string{},
			toWaiting: []scheduler.Transition{{Key: "r", Reasons: []scheduler.Reason{zoneReason("z")}}},
		},
		{
			name: "zone held by publishing", in: scheduler.Input{Tasks: []scheduler.Task{
				task("h", model.TaskStatePublishing, 0, idle, zones("z")),
				task("r", model.TaskStateReady, 1, zones("z")),
			}},
			admit:     []string{},
			toWaiting: []scheduler.Transition{{Key: "r", Reasons: []scheduler.Reason{zoneReason("z")}}},
		},
		{
			name: "zone held by ci", in: scheduler.Input{Tasks: []scheduler.Task{
				task("h", model.TaskStateCI, 0, idle, zones("z")),
				task("r", model.TaskStateReady, 1, zones("z")),
			}},
			admit:     []string{},
			toWaiting: []scheduler.Transition{{Key: "r", Reasons: []scheduler.Reason{zoneReason("z")}}},
		},
		{
			name: "zone held by review", in: scheduler.Input{Tasks: []scheduler.Task{
				task("h", model.TaskStateReview, 0, idle, zones("z")),
				task("r", model.TaskStateReady, 1, zones("z")),
			}},
			admit:     []string{},
			toWaiting: []scheduler.Transition{{Key: "r", Reasons: []scheduler.Reason{zoneReason("z")}}},
		},
		{
			name: "zone held by fixing, which is admitted itself", in: scheduler.Input{Tasks: []scheduler.Task{
				task("h", model.TaskStateFixing, 0, zones("z")),
				task("r", model.TaskStateReady, 1, zones("z")),
			}},
			admit:     []string{"h"},
			toWaiting: []scheduler.Transition{{Key: "r", Reasons: []scheduler.Reason{zoneReason("z")}}},
		},
		{
			name: "zone held by merge", in: scheduler.Input{Tasks: []scheduler.Task{
				task("h", model.TaskStateMerge, 0, idle, zones("z")),
				task("r", model.TaskStateReady, 1, zones("z")),
			}},
			admit:     []string{},
			toWaiting: []scheduler.Transition{{Key: "r", Reasons: []scheduler.Reason{zoneReason("z")}}},
		},
		{
			name: "zone not held by a flagged task", in: scheduler.Input{Tasks: []scheduler.Task{
				task("h", model.TaskStateRunning, 0, idle, zones("z"), flag(model.TaskFlagNeedsOwner)),
				task("r", model.TaskStateReady, 1, zones("z")),
			}},
			admit: []string{"r"},
		},
		{
			name: "zone not held by a done task", in: scheduler.Input{Tasks: []scheduler.Task{
				task("h", model.TaskStateDone, 0, idle, zones("z")),
				task("r", model.TaskStateReady, 1, zones("z")),
			}},
			admit: []string{"r"},
		},
		{
			name: "zones match by exact string", in: scheduler.Input{Tasks: []scheduler.Task{
				task("h", model.TaskStateRunning, 0, idle, zones("internal/core")),
				task("r", model.TaskStateReady, 1, zones("internal/core/scheduler", "Internal/core")),
			}},
			admit: []string{"r"},
		},
		{
			name: "flagged tasks are never admitted", in: scheduler.Input{Tasks: []scheduler.Task{
				task("f", model.TaskStateFixing, 0, flag(model.TaskFlagHold)),
				task("g", model.TaskStateReady, 0, flag(model.TaskFlagFailed)),
				task("w", model.TaskStateWaiting, 0, flag(model.TaskFlagHold)),
			}},
			admit: []string{},
		},
		{
			name: "shared zone in one decision: first admitted, second waits", in: scheduler.Input{Tasks: []scheduler.Task{
				task("b", model.TaskStateReady, 2, zones("z")),
				task("a", model.TaskStateReady, 1, zones("z")),
			}},
			admit:     []string{"a"},
			toWaiting: []scheduler.Transition{{Key: "b", Reasons: []scheduler.Reason{zoneReason("z")}}},
		},
		{
			name: "dependencies open, done and unknown", in: scheduler.Input{Tasks: []scheduler.Task{
				task("open", model.TaskStateRunning, 0, idle),
				task("done", model.TaskStateDone, 0, idle),
				task("r1", model.TaskStateReady, 1, deps("open")),
				task("r2", model.TaskStateReady, 2, deps("done")),
				task("r3", model.TaskStateReady, 3, deps("missing", "done")),
			}},
			admit: []string{"r2"},
			toWaiting: []scheduler.Transition{
				{Key: "r1", Reasons: []scheduler.Reason{depReason("open")}},
				{Key: "r3", Reasons: []scheduler.Reason{depReason("missing")}},
			},
		},
		{
			name: "reasons list dependencies then zones, each once", in: scheduler.Input{Tasks: []scheduler.Task{
				task("h", model.TaskStateReview, 0, idle, zones("y", "x")),
				task("r", model.TaskStateReady, 1, idle, deps("q", "p", "q"), zones("y", "x", "y")),
			}},
			admit: []string{},
			toWaiting: []scheduler.Transition{{Key: "r", Reasons: []scheduler.Reason{
				depReason("p"), depReason("q"), zoneReason("x"), zoneReason("y"),
			}}},
		},
		{
			name: "waiting task with cleared blockers goes back to ready and is admitted", in: scheduler.Input{Tasks: []scheduler.Task{
				task("d", model.TaskStateDone, 0, idle, zones("z")),
				task("w", model.TaskStateWaiting, 1, deps("d"), zones("z")),
				task("v", model.TaskStateWaiting, 2, idle),
			}},
			admit:   []string{"w"},
			toReady: []string{"v", "w"},
		},
		{
			name: "blocked waiting task stays waiting and is not admitted", in: scheduler.Input{Tasks: []scheduler.Task{
				task("h", model.TaskStateCI, 0, idle, zones("z")),
				task("w", model.TaskStateWaiting, 1, zones("z")),
			}},
			admit: []string{},
		},
		{
			name: "planning skips zone and dependency checks", in: scheduler.Input{Tasks: []scheduler.Task{
				task("h", model.TaskStateRunning, 0, idle, zones("z")),
				task("p", model.TaskStatePlanning, 1, zones("z"), deps("missing")),
			}},
			admit: []string{"p"},
		},
		{
			name: "bands: fixing/merge, then ci/review, then ready; oldest then key inside bands 1 and 2", in: scheduler.Input{Tasks: []scheduler.Task{
				task("r", model.TaskStateReady, 0, prio(scheduler.PriorityHigh)),
				task("c", model.TaskStateCI, 1),
				task("v", model.TaskStateReview, 2),
				task("m", model.TaskStateMerge, 3),
				task("f2", model.TaskStateFixing, 3),
				task("f1", model.TaskStateFixing, 5),
				task("fu", model.TaskStateFixing, 6, followup),
			}},
			admit: []string{"f2", "m", "f1", "fu", "c", "v", "r"},
		},
		{
			name: "bands 1 and 2 skip zone and dependency checks", in: scheduler.Input{Tasks: []scheduler.Task{
				task("m", model.TaskStateMerge, 0, zones("z"), deps("missing")),
				task("c", model.TaskStateCI, 1, zones("z"), deps("missing")),
			}},
			admit: []string{"m", "c"},
		},
		{
			name: "band 3: priority, then satisfied dependencies, then oldest, then key; follow-ups last", in: scheduler.Input{Tasks: []scheduler.Task{
				task("d1", model.TaskStateDone, 0, idle),
				task("d2", model.TaskStateDone, 0, idle),
				task("fu-high", model.TaskStateReady, 0, followup, prio(scheduler.PriorityHigh)),
				task("fu-low", model.TaskStateReady, 0, followup, prio(scheduler.PriorityLow)),
				task("low", model.TaskStateReady, 0, prio(scheduler.PriorityLow)),
				task("normal-old", model.TaskStateReady, 1),
				task("normal-new", model.TaskStateReady, 9, prio(scheduler.PriorityNormal)),
				task("normal-2deps", model.TaskStateWaiting, 10, deps("d1", "d2")),
				task("normal-1dep", model.TaskStatePlanning, 10, deps("d1")),
				task("high-b", model.TaskStateReady, 5, prio(scheduler.PriorityHigh)),
				task("high-a", model.TaskStateReady, 5, prio(scheduler.PriorityHigh)),
			}},
			admit:   []string{"high-a", "high-b", "normal-2deps", "normal-1dep", "normal-old", "normal-new", "low", "fu-high", "fu-low"},
			toReady: []string{"normal-2deps"},
		},
		{
			name: "pause admits nothing but still reports transitions", in: scheduler.Input{
				Pause: []string{"owner"},
				Tasks: []scheduler.Task{
					task("f", model.TaskStateFixing, 0, zones("z")),
					task("r", model.TaskStateReady, 1, zones("z")),
					task("w", model.TaskStateWaiting, 2),
					task("p", model.TaskStatePlanning, 3),
				},
			},
			admit:     []string{},
			toWaiting: []scheduler.Transition{{Key: "r", Reasons: []scheduler.Reason{zoneReason("z")}}},
			toReady:   []string{"w"},
		},
		{
			name: "no cap admits every eligible candidate", in: scheduler.Input{
				RunningContainers: 100,
				Tasks: []scheduler.Task{
					task("a", model.TaskStateReady, 0, containers(5)),
					task("b", model.TaskStateCI, 0, containers(7)),
					task("c", model.TaskStatePlanning, 0),
					task("i", model.TaskStateReady, 0, idle),
					task("x", model.TaskStateRunning, 0),
					task("y", model.TaskStateInbox, 0),
					task("d", model.TaskStateDone, 0),
				},
			},
			admit: []string{"b", "a", "c"},
		},
		{
			name: "cap reached admits nothing", in: scheduler.Input{
				MaxConcurrentContainers: capOf(3), RunningContainers: 3,
				Tasks: []scheduler.Task{task("f", model.TaskStateFixing, 0), task("r", model.TaskStateReady, 0)},
			},
			admit: []string{},
		},
		{
			name: "cap fills exactly", in: scheduler.Input{
				MaxConcurrentContainers: capOf(3), RunningContainers: 1,
				Tasks: []scheduler.Task{task("a", model.TaskStateReady, 0), task("b", model.TaskStateReady, 1), task("c", model.TaskStateReady, 2)},
			},
			admit: []string{"a", "b"},
		},
		{
			name: "multi-container candidate that does not fit stops admission", in: scheduler.Input{
				MaxConcurrentContainers: capOf(4), RunningContainers: 1,
				Tasks: []scheduler.Task{
					task("a", model.TaskStateReady, 0),
					task("big", model.TaskStateReady, 1, containers(3)),
					task("small", model.TaskStateReady, 2),
				},
			},
			admit: []string{"a"},
		},
		{
			name: "blocked candidate does not stop admission", in: scheduler.Input{
				MaxConcurrentContainers: capOf(1),
				Tasks: []scheduler.Task{
					task("a", model.TaskStateReady, 0, deps("missing")),
					task("b", model.TaskStateReady, 1),
				},
			},
			admit:     []string{"b"},
			toWaiting: []scheduler.Transition{{Key: "a", Reasons: []scheduler.Reason{depReason("missing")}}},
		},
		{
			name: "cap 0 admits nothing", in: scheduler.Input{
				MaxConcurrentContainers: capOf(0),
				Tasks:                   []scheduler.Task{task("f", model.TaskStateFixing, 0), task("w", model.TaskStateWaiting, 0)},
			},
			admit:   []string{},
			toReady: []string{"w"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := scheduler.Decide(tt.in)
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if got := admitted(d); !reflect.DeepEqual(got, tt.admit) {
				t.Errorf("admit = %v, want %v", got, tt.admit)
			}
			if !reflect.DeepEqual(d.ToWaiting, tt.toWaiting) {
				t.Errorf("toWaiting = %+v, want %+v", d.ToWaiting, tt.toWaiting)
			}
			want := tt.toReady
			if want == nil {
				want = []string{}
			}
			if got := keys(d.ToReady); !reflect.DeepEqual(got, want) {
				t.Errorf("toReady = %v, want %v", got, want)
			}
		})
	}
}

func TestDecide_AdmissionCarriesStateAndContainers(t *testing.T) {
	d, err := scheduler.Decide(scheduler.Input{Tasks: []scheduler.Task{task("a", model.TaskStateReview, 0, containers(2))}})
	if err != nil {
		t.Fatal(err)
	}
	want := []scheduler.Admission{{Key: "a", State: model.TaskStateReview, Containers: 2}}
	if !reflect.DeepEqual(d.Admit, want) {
		t.Errorf("admit = %+v, want %+v", d.Admit, want)
	}
}

// mixed is an input that exercises every rule at once.
func mixed() scheduler.Input {
	return scheduler.Input{
		MaxConcurrentContainers: capOf(6),
		RunningContainers:       1,
		Tasks: []scheduler.Task{
			task("done", model.TaskStateDone, 0, idle, zones("old")),
			task("run", model.TaskStateRunning, 0, idle, zones("busy")),
			task("fix", model.TaskStateFixing, 3, zones("f")),
			task("ci", model.TaskStateCI, 2),
			task("r-high", model.TaskStateReady, 5, prio(scheduler.PriorityHigh), zones("s")),
			task("r-same-1", model.TaskStateReady, 7, zones("s")),
			task("r-same-2", model.TaskStateReady, 7),
			task("r-dep", model.TaskStateReady, 7, deps("done")),
			task("r-busy", model.TaskStateReady, 1, zones("busy")),
			task("w-clear", model.TaskStateWaiting, 4, deps("done")),
			task("fu", model.TaskStateReady, 0, followup),
			task("plan", model.TaskStatePlanning, 8, prio(scheduler.PriorityLow)),
			task("held", model.TaskStateReady, 0, flag(model.TaskFlagHold)),
		},
	}
}

func TestDecide_DeterministicForAnyInputOrder(t *testing.T) {
	in := mixed()
	want, err := scheduler.Decide(in)
	if err != nil {
		t.Fatal(err)
	}
	if got := admitted(want); !reflect.DeepEqual(got, []string{"fix", "ci", "r-high", "w-clear", "r-dep"}) {
		t.Fatalf("admit = %v", got)
	}
	n := len(in.Tasks)
	for _, step := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12} {
		shuffled := in
		shuffled.Tasks = make([]scheduler.Task, n)
		// Fixed permutation: reverse, then rotate by step.
		for i := range in.Tasks {
			shuffled.Tasks[(n-1-i+step)%n] = in.Tasks[i]
		}
		got, err := scheduler.Decide(shuffled)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("step %d: decision = %+v, want %+v", step, got, want)
		}
	}
}

func TestDecide_HostCapacityNeverChangesDecision(t *testing.T) {
	in := mixed()
	want, err := scheduler.Decide(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []ports.Capacity{
		{},
		{CPUMillis: 1, MemoryBytes: 1, DiskBytes: 1},
		{CPUMillis: 1 << 40, MemoryBytes: 1 << 50, DiskBytes: 1 << 60},
		{CPUMillis: -1, MemoryBytes: -1, DiskBytes: -1},
	} {
		in.HostCapacity = c
		got, err := scheduler.Decide(in)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("capacity %+v: decision = %+v, want %+v", c, got, want)
		}
	}
}

func TestDecide_DoesNotMutateInput(t *testing.T) {
	build := func() scheduler.Input {
		in := mixed()
		in.Pause = []string{"b", "a"}
		in.Tasks[4].Zones = []string{"z", "s", "z"}
		in.Tasks[7].Dependencies = []model.TaskKey{"x", "done", "a"}
		return in
	}
	in := build()
	if _, err := scheduler.Decide(in); err != nil {
		t.Fatal(err)
	}
	if want := build(); !reflect.DeepEqual(in, want) {
		t.Errorf("input mutated:\n got %+v\nwant %+v", in, want)
	}
}

func TestDecide_InputErrors(t *testing.T) {
	ok := func(k string) scheduler.Task { return task(k, model.TaskStateReady, 0) }
	tests := []struct {
		name, want string
		in         scheduler.Input
	}{
		{"duplicate key", "duplicate task key", scheduler.Input{Tasks: []scheduler.Task{ok("a"), ok("a")}}},
		{"unknown state", "task state", scheduler.Input{Tasks: []scheduler.Task{task("a", "paused", 0)}}},
		{"empty state", "task state", scheduler.Input{Tasks: []scheduler.Task{task("a", "", 0)}}},
		{"unknown flag", "task flag", scheduler.Input{Tasks: []scheduler.Task{task("a", model.TaskStateReady, 0, flag("blocked"))}}},
		{"unknown priority", "unknown priority", scheduler.Input{Tasks: []scheduler.Task{task("a", model.TaskStateReady, 0, prio("urgent"))}}},
		{"unknown kind", "unknown kind", scheduler.Input{Tasks: []scheduler.Task{task("a", model.TaskStateReady, 0, func(t *scheduler.Task) { t.Kind = "bugfix" })}}},
		{"zero containers", "containers", scheduler.Input{Tasks: []scheduler.Task{task("a", model.TaskStateReady, 0, containers(0))}}},
		{"negative containers", "containers", scheduler.Input{Tasks: []scheduler.Task{task("a", model.TaskStateReady, 0, containers(-1))}}},
		{"negative cap", "negative cap", scheduler.Input{MaxConcurrentContainers: capOf(-1), Tasks: []scheduler.Task{ok("a")}}},
		{"negative running count", "negative running", scheduler.Input{RunningContainers: -1, Tasks: []scheduler.Task{ok("a")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := scheduler.Decide(tt.in)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
			if !reflect.DeepEqual(d, scheduler.Decision{}) {
				t.Errorf("decision = %+v, want zero on error", d)
			}
		})
	}
}

func TestDecide_ZeroContainersAllowedWithoutRun(t *testing.T) {
	if _, err := scheduler.Decide(scheduler.Input{Tasks: []scheduler.Task{task("a", model.TaskStateDone, 0, idle)}}); err != nil {
		t.Fatalf("Decide: %v", err)
	}
}
