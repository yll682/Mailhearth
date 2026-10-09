package core

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"

	"mailhearth/internal/db"
	"mailhearth/internal/mailproto/imappool"
	"mailhearth/internal/provider"
)

type ResolvedEndpoint struct {
	Protocol string
	Network provider.ProtocolTemplate
	Username string
	Secret string
	CredentialID int64
	CredentialGeneration int64
	ConnectionRevision int64
	EndpointRevision int64
	OrgID int64
	ConnectionID int64
	MailboxID int64
	TLSConfig *tls.Config
	Dialer *net.Dialer
}

func (e *ResolvedEndpoint) Address() string{return net.JoinHostPort(e.Network.Host,strconv.Itoa(e.Network.Port))}
func (e *ResolvedEndpoint) IMAPCredential() imappool.Cred {
	return imappool.Cred{User:e.Username,Pass:e.Secret,OrgID:e.OrgID,ConnectionID:e.ConnectionID,MailboxID:e.MailboxID,ConnectionRevision:e.ConnectionRevision,EndpointRevision:e.EndpointRevision,CredentialID:e.CredentialID,CredentialGeneration:e.CredentialGeneration,Addr:e.Address(),TLSMode:e.Network.TLSMode,TLSConfig:e.TLSConfig,Dialer:e.Dialer}
}

func templateFor(templates provider.ProtocolTemplates,protocol string) (provider.ProtocolTemplate,error) {
	switch protocol{case provider.ProtocolIMAP:return templates.IMAP,nil;case provider.ProtocolSMTP:return templates.SMTP,nil;case provider.ProtocolManageSieve:return templates.ManageSieve,nil}
	return provider.ProtocolTemplate{},provider.Errorf("invalid","协议名称无效")
}

func (s *Service) ResolveEndpoint(ctx context.Context,orgID,mailboxID int64,protocol string) (*ResolvedEndpoint,error) {
	var e ResolvedEndpoint
	var mode,defaults string
	var network,user,sealed,state sql.NullString
	var credentialID,generation sql.NullInt64
	var enabled bool
	err:=s.DB.QueryRowContext(ctx,`SELECT mc.id,mc.enabled,mc.revision,mc.protocol_defaults_json,ep.network_mode,ep.network_override_json,ep.username,ep.credential_id,ep.revision,
		cr.secret_enc,cr.generation,cr.state FROM mailboxes b JOIN mail_connections mc ON mc.id=b.connection_id AND mc.org_id=b.org_id JOIN mailbox_endpoints ep ON ep.mailbox_id=b.id
		LEFT JOIN credentials cr ON cr.id=ep.credential_id AND cr.mailbox_id=b.id AND cr.connection_id=mc.id AND cr.purpose='mail'
		WHERE b.org_id=? AND b.id=? AND ep.protocol=?`,orgID,mailboxID,protocol).Scan(&e.ConnectionID,&enabled,&e.ConnectionRevision,&defaults,&mode,&network,&user,&credentialID,&e.EndpointRevision,&sealed,&generation,&state)
	if db.IsNotFound(err){return nil,provider.Errorf("endpoint_unconfigured","协议尚未配置")};if err!=nil{return nil,err}
	if !enabled || mode=="disabled"{return nil,provider.Errorf("endpoint_disabled","协议已停用")}
	var templates provider.ProtocolTemplates
	if err:=json.Unmarshal([]byte(defaults),&templates);err!=nil{return nil,err}
	if mode=="inherit"{e.Network,err=templateFor(templates,protocol);if err!=nil{return nil,err}}else if mode=="override"{
		if !network.Valid{return nil,provider.Errorf("endpoint_unconfigured","协议网络配置缺失")}
		if err:=json.Unmarshal([]byte(network.String),&e.Network);err!=nil{return nil,err}
	}else{return nil,provider.Errorf("invalid","协议配置模式无效")}
	if !e.Network.Enabled || !user.Valid || user.String=="" || !credentialID.Valid || !sealed.Valid || sealed.String=="" || state.String!="active"{return nil,provider.Errorf("endpoint_unconfigured","协议需要用户名及有效凭据")}
	e.Secret,err=s.Box.Open(sealed.String);if err!=nil{return nil,provider.Errorf("credential_decryption_failed","邮件凭据解密失败")}
	e.Protocol,e.Username,e.CredentialID,e.CredentialGeneration,e.OrgID,e.MailboxID=protocol,user.String,credentialID.Int64,generation.Int64,orgID,mailboxID
	if err:=s.prepareEndpointNetwork(ctx,&e);err!=nil{return nil,err}
	return &e,nil
}

func (s *Service) prepareEndpointNetwork(ctx context.Context,e *ResolvedEndpoint) error {
	if e.Network.TLSMode!="none" || !s.Cfg.DevStack{if err:=provider.ValidateProtocolTemplate(e.Network);err!=nil{return err}}
	e.TLSConfig=&tls.Config{ServerName:e.Network.Host,MinVersion:tls.VersionTLS12}
	if e.Network.CABundleID!=nil{
		roots:=s.Cfg.CABundles[*e.Network.CABundleID];if roots==nil{return provider.Errorf("invalid","CA bundle 未配置")};e.TLSConfig.RootCAs=roots
	}
	lookupCtx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
	ips,err:=net.DefaultResolver.LookupIPAddr(lookupCtx,e.Network.Host);if err!=nil{return provider.Errorf("upstream_failed","邮件服务器 DNS 查询失败")}
	if len(ips)==0{return provider.Errorf("upstream_failed","邮件服务器没有 IP 地址")}
	allowed:=map[string]bool{}
	for _,ip:=range ips{
		if !s.allowedMailIP(ip.IP){return provider.Errorf("invalid","邮件服务器地址未获部署者授权")}
		allowed[ip.IP.String()]=true
	}
	e.Dialer=&net.Dialer{Timeout:10*time.Second,ControlContext:func(ctx context.Context,network,address string,raw syscall.RawConn)error{
		host,_,err:=net.SplitHostPort(address);if err!=nil{return err}
		if !allowed[host]{return provider.Errorf("invalid","邮件服务器 IP 与已验证地址不一致")};return nil
	}}
	return nil
}

func (s *Service) allowedMailIP(ip net.IP) bool {
	if s.Cfg.DevStack && ip.IsLoopback(){return true}
	for _,network:=range s.Cfg.AllowedMailNetworks{if network.Contains(ip){return true}}
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast(){return false}
	if v4:=ip.To4();v4!=nil{
		if v4[0]==0 || v4[0]==127 || v4[0]>=224 || (v4[0]==100 && v4[1]>=64 && v4[1]<=127) || (v4[0]==198 && (v4[1]==18 || v4[1]==19)){return false}
	}
	return true
}

func validProtocolUsername(value string) bool{return value!="" && !strings.ContainsAny(value,"\x00\r\n")}
