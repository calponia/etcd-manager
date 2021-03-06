package backupcontroller

import (
	"context"
	"fmt"
	"time"

	"k8s.io/klog"

	"kope.io/etcd-manager/pkg/backup"
)

// BackupCleanup encapsulates the logic around periodically removing old backups
type BackupCleanup struct {
	backupStore backup.Store

	// lastBackupCleanup is the time at which we last performed a backup store cleanup (as leader)
	lastBackupCleanup time.Time

	backupCleanupInterval time.Duration
}

// NewBackupCleanup constructs a BackupCleanup
func NewBackupCleanup(backupStore backup.Store) *BackupCleanup {
	return &BackupCleanup{
		backupStore:           backupStore,
		backupCleanupInterval: time.Hour,
	}
}

// MaybeDoBackupMaintenance removes old backups, if a suitable interval has passed.
// It should be called periodically, after every backup for example.
func (m *BackupCleanup) MaybeDoBackupMaintenance(ctx context.Context) error {
	now := time.Now()

	if now.Sub(m.lastBackupCleanup) < m.backupCleanupInterval {
		return nil
	}

	backupNames, err := m.backupStore.ListBackups()
	if err != nil {
		return fmt.Errorf("error listing backups: %v", err)
	}

	minRetention := time.Hour
	hourly := time.Hour * 24 * 7
	daily := time.Hour * 24 * 7 * 365

	backups := make(map[time.Time]string)
	retain := make(map[string]bool)
	ignore := make(map[string]bool)
	buckets := make(map[time.Time]time.Time)

	for _, backup := range backupNames {
		// Time parsing uses the same layout values as `Format`.
		i := parseBackupNameInfo(backup)
		if i == nil {
			klog.Warningf("ignoring unparseable backup %q", backup)
			ignore[backup] = true
			continue
		}

		t := i.Timestamp
		backups[t] = backup

		age := now.Sub(t)

		if age < minRetention {
			retain[backup] = true
			continue
		}

		if age < hourly {
			bucketed := t.Truncate(time.Hour)
			existing := buckets[bucketed]
			if existing.IsZero() || existing.After(t) {
				buckets[bucketed] = t
			}
			continue
		}

		if age < daily {
			bucketed := t.Truncate(time.Hour * 24)
			existing := buckets[bucketed]
			if existing.IsZero() || existing.After(t) {
				buckets[bucketed] = t
			}
			continue
		}
	}

	for _, t := range buckets {
		retain[backups[t]] = true
	}

	removedCount := 0
	for _, backup := range backupNames {
		if retain[backup] {
			klog.V(4).Infof("retaining backup %q", backup)
			continue
		}
		if ignore[backup] {
			klog.V(4).Infof("ignoring backup %q", backup)
			continue
		}
		klog.V(4).Infof("removing backup %q", backup)
		if err := m.backupStore.RemoveBackup(backup); err != nil {
			klog.Warningf("failed to remove backup %q: %v", backup, err)
		} else {
			klog.V(2).Infof("removed backup %q", backup)
			removedCount++
		}
	}

	if removedCount != 0 {
		klog.Infof("Removed %d old backups", removedCount)
	}

	m.lastBackupCleanup = now

	return nil
}
