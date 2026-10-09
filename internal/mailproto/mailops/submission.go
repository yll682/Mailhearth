package mailops

import (
	"bytes"
	"context"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-message"
	"mailhearth/internal/mailproto/imappool"
	"mailhearth/internal/provider"
)

type MessageLocator struct {
	Folder string `json:"folder"`
	UIDValidity uint32 `json:"uidValidity"`
	UID uint32 `json:"uid"`
}

func DeleteLocatedMessage(ctx context.Context,conn *imappool.Conn,locator MessageLocator) error {
	conn.Invalidate()
	selected,err:=conn.Select(ctx,locator.Folder,false);if err!=nil{return err}
	if locator.UID==0 || locator.UIDValidity==0 || selected.UIDValidity!=locator.UIDValidity{return provider.Errorf("draft_locator_changed","邮件定位信息已变化")}
	caps:=conn.C.Caps()
	if !caps.Has(imap.CapUIDPlus) && !caps.Has(imap.CapIMAP4rev2){return provider.Errorf("unsupported_operation","清理草稿需要 UID EXPUNGE 支持")}
	return Delete(ctx,conn,locator.Folder,[]uint32{locator.UID},"",true)
}

func ReadSubmissionMessage(ctx context.Context,conn *imappool.Conn,locator MessageLocator,id string,maxBytes int64) ([]byte,error) {
	conn.Invalidate()
	selected,err:=conn.Select(ctx,locator.Folder,true);if err!=nil{return nil,err}
	if locator.UID==0 || locator.UIDValidity==0 || selected.UIDValidity!=locator.UIDValidity{return nil,provider.Errorf("draft_locator_changed","邮件定位信息已变化")}
	raw,err:=RawMessage(ctx,conn,locator.Folder,locator.UID,maxBytes);if err!=nil{return nil,err}
	entity,err:=message.Read(bytes.NewReader(raw));if err!=nil{return nil,err}
	if entity.Header.Get("X-Mailhearth-Submission-ID")!=id{return nil,provider.Errorf("submission_content_changed","提交标识与邮件内容不一致")}
	return raw,nil
}

func FindSubmissionMessage(ctx context.Context,conn *imappool.Conn,folder,id string,maxBytes int64) (*MessageLocator,error) {
	conn.Invalidate()
	selected,err:=conn.Select(ctx,folder,true);if err!=nil{return nil,err}
	var found *imap.SearchData
	conn.Run(ctx,func(){found,err=conn.C.UIDSearch(&imap.SearchCriteria{Header:[]imap.SearchCriteriaHeaderField{{Key:"X-Mailhearth-Submission-ID",Value:id}}},nil).Wait()})
	if err!=nil{return nil,err}
	uids:=found.AllUIDs();if len(uids)>100{return nil,provider.Errorf("submission_copy_conflict","提交标识对应过多邮件")}
	var result *MessageLocator
	for _,uid:=range uids {
		locator:=MessageLocator{Folder:folder,UIDValidity:selected.UIDValidity,UID:uint32(uid)}
		raw,err:=RawMessage(ctx,conn,folder,uint32(uid),maxBytes);if err!=nil{return nil,err}
		entity,err:=message.Read(bytes.NewReader(raw));if err!=nil{return nil,err}
		if entity.Header.Get("X-Mailhearth-Submission-ID")!=id{continue}
		if result!=nil{return nil,provider.Errorf("submission_copy_conflict","同一提交标识存在多项邮件")}
		result=&locator
	}
	return result,nil
}

func AppendSubmissionMessage(ctx context.Context,conn *imappool.Conn,folder,id string,flags []string,raw []byte,maxBytes int64) (*MessageLocator,error) {
	if _,err:=Append(ctx,conn,folder,flags,time.Now(),raw);err!=nil{return nil,err}
	locator,err:=FindSubmissionMessage(ctx,conn,folder,id,maxBytes);if err!=nil{return nil,err}
	if locator==nil{return nil,provider.Errorf("submission_copy_unknown","保存结果需要核查")}
	return locator,nil
}
