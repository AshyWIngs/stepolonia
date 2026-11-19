package identity

import (
	"context"
	"errors"
	"testing"
)

func TestAuthBearerShape(t *testing.T) {
	token, err := bearer()
	if err != nil {
		t.Fatal(err)
	}
	if !validBearer(token) || len(digest(token)) != 32 {
		t.Fatal("bearer shape")
	}
	for _, invalid := range []string{"", token + "=", token[:42], "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA!"} {
		if validBearer(invalid) {
			t.Fatal("accepted malformed bearer")
		}
	}
}
func TestAuthHashCapacityAndDummy(t *testing.T) {
	for i := 0; i < cap(bootstrapHashSlots); i++ {
		bootstrapHashSlots <- struct{}{}
	}
	_, err := bootstrapHashSlot(context.Background())
	if err == nil {
		t.Fatal("KDF capacity exceeded")
	}
	for i := 0; i < cap(bootstrapHashSlots); i++ {
		<-bootstrapHashSlots
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = bootstrapHashSlot(canceled)
	if err == nil {
		<-bootstrapHashSlots
	} else if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if verifyPassword("any ordinary password", "$argon2id$v=19$m=19456,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA") {
		t.Fatal("dummy accepted")
	}
}

func TestAuthHashSoftCapacityReservesRecoveryHeadroom(t *testing.T) {
	ctx := context.Background()
	release, err := passwordHashSlot(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if extra, err := passwordHashSlot(ctx, 1); !errors.Is(err, ErrRateLimited) || RateRetryAfter(err) != 1 {
		if extra != nil {
			extra()
		}
		t.Fatalf("public password work exceeded its configured cap: %v", err)
	}
	recoverSlot, err := passwordHashSlot(ctx, MaxPasswordHashJobs)
	if err != nil {
		t.Fatalf("trusted work could not use spare process capacity: %v", err)
	}
	recoverSlot()
}
