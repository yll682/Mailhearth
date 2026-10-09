package core

import (
	"context"
	"sync"
	"testing"
)

func TestMultiProviderStorageMemberProfileRevision(t *testing.T) {
	s, m := newStorageService(t)
	ctx := context.Background()
	member := storageOffboardMember(t, s, m, "成员资料版本")
	_, err := s.UpdateMember(ctx, m.OrgID, m.ID, member.ID, MemberInput{DisplayName: "缺少版本"})
	requireProviderCode(t, err, "revision_conflict")
	var workers sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"并发修改一", "并发修改二"} {
		workers.Add(1)
		go func(name string) {
			defer workers.Done()
			_, err := s.UpdateMember(ctx, m.OrgID, m.ID, member.ID, MemberInput{DisplayName: name, ExpectedRevision: member.Revision})
			results <- err
		}(name)
	}
	workers.Wait()
	close(results)
	completed := 0
	for err := range results {
		if err == nil {
			completed++
		} else {
			requireProviderCode(t, err, "revision_conflict")
		}
	}
	if completed != 1 {
		t.Fatalf("相同版本提交了 %d 项成员更新", completed)
	}
	current, err := s.Member(ctx, m.OrgID, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision != member.Revision+1 {
		t.Fatal("成员更新没有增加唯一版本")
	}
}
