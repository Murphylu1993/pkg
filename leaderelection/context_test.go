// +build !race
// TODO(https://github.com/kubernetes/kubernetes/issues/90952): Remove the above.

/*
Copyright 2020 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package leaderelection

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	fakekube "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"knative.dev/pkg/reconciler"
	_ "knative.dev/pkg/system/testing"
)

func TestWithBuilder(t *testing.T) {
	cc := ComponentConfig{
		Component:     "component",
		LeaderElect:   true,
		Buckets:       1,
		ResourceLock:  "leases",
		LeaseDuration: 15 * time.Second,
		RenewDeadline: 10 * time.Second,
		RetryPeriod:   2 * time.Second,
	}
	kc := fakekube.NewSimpleClientset()
	ctx := context.Background()

	promoted := make(chan struct{})
	demoted := make(chan struct{})
	laf := &reconciler.LeaderAwareFuncs{
		PromoteFunc: func(bkt reconciler.Bucket, enq func(reconciler.Bucket, types.NamespacedName)) error {
			close(promoted)
			return nil
		},
		DemoteFunc: func(bkt reconciler.Bucket) {
			close(demoted)
		},
	}
	enq := func(reconciler.Bucket, types.NamespacedName) {}

	created := make(chan struct{})
	kc.PrependReactor("create", "leases",
		func(action ktesting.Action) (bool, runtime.Object, error) {
			close(created)
			return false, nil, nil
		},
	)

	updated := make(chan struct{})
	kc.PrependReactor("update", "leases",
		func(action ktesting.Action) (bool, runtime.Object, error) {
			// Only close update once.
			select {
			case <-updated:
			default:
				close(updated)
			}
			return false, nil, nil
		},
	)

	if HasLeaderElection(ctx) {
		t.Error("HasLeaderElection() = true, wanted false")
	}
	if le, err := BuildElector(ctx, laf, "name", enq); err != nil {
		t.Errorf("BuildElector() = %v, wanted an unopposedElector", err)
	} else if _, ok := le.(*unopposedElector); !ok {
		t.Errorf("BuildElector() = %T, wanted an unopposedElector", le)
	}

	ctx = WithStandardLeaderElectorBuilder(ctx, kc, cc)
	if !HasLeaderElection(ctx) {
		t.Error("HasLeaderElection() = false, wanted true")
	}

	le, err := BuildElector(ctx, laf, "name", enq)
	if err != nil {
		t.Errorf("BuildElector() = %v", err)
	}

	// We shouldn't see leases until we Run the elector.
	select {
	case <-promoted:
		t.Error("Got promoted, want no actions.")
	case <-demoted:
		t.Error("Got demoted, want no actions.")
	case <-created:
		t.Error("Got created, want no actions.")
	case <-updated:
		t.Error("Got updated, want no actions.")
	default:
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go le.Run(ctx)

	select {
	case <-created:
		// We expect the lease to be created.
	case <-time.After(1 * time.Second):
		t.Fatal("Timed out waiting for lease creation.")
	}
	select {
	case <-promoted:
		// We expect to have been promoted.
	case <-time.After(1 * time.Second):
		t.Fatal("Timed out waiting for promotion.")
	}

	// Cancelling the context should case us to give up leadership.
	cancel()

	select {
	case <-updated:
		// We expect the lease to be updated.
	case <-time.After(1 * time.Second):
		t.Fatal("Timed out waiting for lease update.")
	}
	select {
	case <-demoted:
		// We expect to have been demoted.
	case <-time.After(1 * time.Second):
		t.Fatal("Timed out waiting for demotion.")
	}
}
