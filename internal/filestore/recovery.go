package filestore

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

const TransactionPrefix = ".upload-txn-"
const CommitMarker = "lan-copy-upload-committed\n"

type TransactionRecord struct {
	Destination string   `json:"destination"`
	Roots       []string `json:"roots"`
	Replaced    []string `json:"replaced"`
}

type ReceiveRecord struct {
	ID          string `json:"id"`
	Destination string `json:"destination"`
	State       string `json:"state"`
}

type RecoveryRequired struct{ cause error }

func (e *RecoveryRequired) Error() string {
	return "upload rollback requires startup recovery: " + e.cause.Error()
}
func (e *RecoveryRequired) Unwrap() error { return e.cause }

func WriteDurableFile(name string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(name))
}

// recoverUploadTransactions rolls back commits interrupted by process exit.
// The transaction directory shares the storage filesystem, so each individual
// rename is atomic and its state can be inferred from stage/backup/target.
func RecoverTransactions(sharedDir, dataDir string) error {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return err
	}
	resolver := New(sharedDir, dataDir)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), TransactionPrefix) {
			continue
		}
		transaction := filepath.Join(dataDir, entry.Name())
		var receive ReceiveRecord
		if data, readErr := os.ReadFile(filepath.Join(transaction, "receive.json")); readErr == nil {
			if err := json.Unmarshal(data, &receive); err != nil || receive.ID == "" || receive.State != "receiving" {
				log.Printf("异常: 上传接收记录无效，事务目录=%s 错误=%v", entry.Name(), err)
				receive = ReceiveRecord{}
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return fmt.Errorf("read upload receive record %s: %w", entry.Name(), readErr)
		}
		if marker, err := os.ReadFile(filepath.Join(transaction, "committed")); err == nil && string(marker) == CommitMarker {
			if err := os.RemoveAll(transaction); err != nil {
				return fmt.Errorf("clean committed upload transaction %s: %w", entry.Name(), err)
			}
			continue
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		recordPath := filepath.Join(transaction, "transaction.json")
		data, readErr := os.ReadFile(recordPath)
		if errors.Is(readErr, os.ErrNotExist) {
			// No target mutation can happen before the durable record exists.
			id, targetName := receive.ID, receive.Destination
			if id == "" {
				id = strings.TrimPrefix(entry.Name(), TransactionPrefix)
			}
			log.Printf("异常: 检测到中断上传，事务=%s 目标=%q 状态=%s；临时数据将清理", id, targetName, receive.State)
			if err := os.RemoveAll(transaction); err != nil {
				log.Printf("异常: 清理中断上传失败，事务目录=%s 错误=%v", entry.Name(), err)
				return err
			}
			continue
		}
		if readErr != nil {
			return fmt.Errorf("read %s: %w", entry.Name(), readErr)
		}
		var record TransactionRecord
		if err := json.Unmarshal(data, &record); err != nil || len(record.Roots) == 0 {
			return fmt.Errorf("invalid upload transaction record %s", entry.Name())
		}
		target, err := resolver.Path(record.Destination)
		if err != nil {
			return fmt.Errorf("resolve upload transaction target %s: %w", entry.Name(), err)
		}
		stage := filepath.Join(transaction, "stage")
		backup := filepath.Join(transaction, "backup")
		replaced := make(map[string]bool, len(record.Replaced))
		for _, root := range record.Replaced {
			if err := ValidateComponent(root); err != nil {
				return fmt.Errorf("invalid replaced root in %s", entry.Name())
			}
			replaced[root] = true
		}
		for i := len(record.Roots) - 1; i >= 0; i-- {
			root := record.Roots[i]
			if err := ValidateComponent(root); err != nil {
				return fmt.Errorf("invalid root in %s", entry.Name())
			}
			targetRoot := filepath.Join(target, root)
			backupRoot := filepath.Join(backup, root)
			if _, err := os.Lstat(backupRoot); err == nil {
				if err := os.RemoveAll(targetRoot); err != nil {
					return fmt.Errorf("remove interrupted upload %s: %w", root, err)
				}
				if err := os.Rename(backupRoot, targetRoot); err != nil {
					return fmt.Errorf("restore replaced upload %s: %w", root, err)
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			} else if !replaced[root] {
				if _, stageErr := os.Lstat(filepath.Join(stage, root)); errors.Is(stageErr, os.ErrNotExist) {
					if err := os.RemoveAll(targetRoot); err != nil {
						return fmt.Errorf("remove interrupted upload %s: %w", root, err)
					}
				} else if stageErr != nil {
					return stageErr
				}
			}
		}
		if err := os.RemoveAll(transaction); err != nil {
			log.Printf("异常: 清理已恢复上传事务失败，事务目录=%s 错误=%v", entry.Name(), err)
			return fmt.Errorf("remove recovered upload transaction %s: %w", entry.Name(), err)
		}
		log.Printf("异常恢复: 已回滚未完成上传，事务目录=%s", entry.Name())
	}
	return nil
}
