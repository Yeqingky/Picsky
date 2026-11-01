package service_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/yeqingky/Picsky/internal/model"
	"github.com/yeqingky/Picsky/internal/service"
)

func TestConcurrentFirstRegistrationsCreateOneAdmin(t *testing.T) {
	f := newFixture(t)
	if err := f.database.Exec("DELETE FROM users").Error; err != nil {
		t.Fatalf("清空用户失败: %v", err)
	}

	const registrations = 8
	errs := make(chan error, registrations)
	var wg sync.WaitGroup
	for i := range registrations {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.svc.Auth.Register(context.Background(), service.RegisterInput{
				Username: fmt.Sprintf("race%d", i),
				Email:    fmt.Sprintf("race%d@example.com", i),
				Password: "password123",
			}, "127.0.0.1", "test-agent")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("并发注册失败: %v", err)
		}
	}

	var adminGroup model.Group
	if err := f.database.Where("name = ?", "管理员").First(&adminGroup).Error; err != nil {
		t.Fatalf("读取管理员组失败: %v", err)
	}
	var admins int64
	if err := f.database.Model(&model.User{}).Where("group_id = ?", adminGroup.ID).Count(&admins).Error; err != nil {
		t.Fatalf("统计管理员失败: %v", err)
	}
	if admins != 1 {
		t.Errorf("并发注册后管理员数量 = %d, 期望 1", admins)
	}
}
