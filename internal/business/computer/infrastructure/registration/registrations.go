// Package registration keeps pending registrations of Managed Computers in
// Redis until they expire.
package registration

import (
	"context"
	"errors"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/computer/application"
)

const (
	userCodeField    = "user_code"
	computerIDField  = "computer_id"
	machineNameField = "machine_name"
	approvedByField  = "approved_by"
	expiresAtField   = "expires_at"
)

func deviceKey(digest string) string { return "computer:registration:device:" + digest }
func userCodeKey(code string) string { return "computer:registration:user:" + code }

// Store holds pending registrations: one hash per device code digest, and the
// user code pointing at it, both expiring with the registration.
type Store struct{ rdb goredis.UniversalClient }

var _ application.Registrations = (*Store)(nil)

func NewStore(rdb goredis.UniversalClient) *Store { return &Store{rdb: rdb} }

func (s *Store) Open(ctx context.Context, pending application.PendingRegistration) error {
	ttl := time.Until(pending.ExpiresAt)
	if ttl <= 0 {
		return oops.Wrap(application.ErrRegistrationNotFound)
	}
	taken, err := s.rdb.SetNX(ctx, userCodeKey(pending.UserCode), pending.DeviceDigest, ttl).Result()
	if err != nil {
		return oops.With("operation", "registration.open").Wrap(err)
	}
	if !taken {
		return application.ErrUserCodeTaken
	}
	pipe := s.rdb.TxPipeline()
	pipe.HSet(ctx, deviceKey(pending.DeviceDigest), userCodeField, pending.UserCode, computerIDField, pending.ComputerID,
		machineNameField, pending.MachineName, approvedByField, "", expiresAtField, pending.ExpiresAt.UnixMilli())
	pipe.PExpireAt(ctx, deviceKey(pending.DeviceDigest), pending.ExpiresAt)
	if _, err := pipe.Exec(ctx); err != nil {
		// The user code must not outlive a registration that was never kept.
		if cleanup := s.rdb.Del(context.WithoutCancel(ctx), userCodeKey(pending.UserCode)).Err(); cleanup != nil {
			err = errors.Join(err, cleanup)
		}
		return oops.With("operation", "registration.open").Wrap(err)
	}
	return nil
}

func (s *Store) ByUserCode(ctx context.Context, userCode string) (application.PendingRegistration, error) {
	digest, err := s.rdb.Get(ctx, userCodeKey(userCode)).Result()
	if errors.Is(err, goredis.Nil) {
		return application.PendingRegistration{}, application.ErrRegistrationNotFound
	}
	if err != nil {
		return application.PendingRegistration{}, oops.With("operation", "registration.by_user_code").Wrap(err)
	}
	return s.ByDevice(ctx, digest)
}

func (s *Store) ByDevice(ctx context.Context, digest string) (application.PendingRegistration, error) {
	values, err := s.rdb.HGetAll(ctx, deviceKey(digest)).Result()
	if err != nil {
		return application.PendingRegistration{}, oops.With("operation", "registration.by_device").Wrap(err)
	}
	if values[computerIDField] == "" {
		return application.PendingRegistration{}, application.ErrRegistrationNotFound
	}
	expiresAt, err := strconv.ParseInt(values[expiresAtField], 10, 64)
	if err != nil {
		return application.PendingRegistration{}, oops.With("operation", "registration.by_device").Wrap(err)
	}
	return application.PendingRegistration{
		DeviceDigest: digest, UserCode: values[userCodeField], ComputerID: values[computerIDField], MachineName: values[machineNameField],
		ApprovedBy: values[approvedByField], ExpiresAt: time.UnixMilli(expiresAt).UTC(),
	}, nil
}

// approveScript records the approver of a registration still kept, once.
var approveScript = goredis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then return 0 end
local approver = redis.call('HGET', KEYS[1], 'approved_by')
if approver == '' or approver == ARGV[1] then
  redis.call('HSET', KEYS[1], 'approved_by', ARGV[1])
  return 1
end
return 2`)

func (s *Store) Approve(ctx context.Context, userCode, personID string) error {
	digest, err := s.rdb.Get(ctx, userCodeKey(userCode)).Result()
	if errors.Is(err, goredis.Nil) {
		return application.ErrRegistrationNotFound
	}
	if err != nil {
		return oops.With("operation", "registration.approve").Wrap(err)
	}
	result, err := approveScript.Run(ctx, s.rdb, []string{deviceKey(digest)}, personID).Int()
	if err != nil {
		return oops.With("operation", "registration.approve").Wrap(err)
	}
	switch result {
	case 1:
		return nil
	case 2:
		return application.ErrRegistrationTaken
	default:
		return application.ErrRegistrationNotFound
	}
}

func (s *Store) Close(ctx context.Context, pending application.PendingRegistration) error {
	return oops.With("operation", "registration.close").Wrap(s.rdb.Del(ctx, deviceKey(pending.DeviceDigest), userCodeKey(pending.UserCode)).Err())
}
