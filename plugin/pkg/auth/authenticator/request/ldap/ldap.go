/*
Copyright 2015 The Kubernetes Authors All rights reserved.

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

package ldap

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/go-ldap/ldap"
	"k8s.io/kubernetes/pkg/auth/user"
)

// LDAP authenticator binds to LDAP server to validate and search user's
type LDAPAuthenticator struct {
	AuthURL         string // e.g. ldap.example.com:389
	BaseDN          string // e.g. dc=some,dc=com
	UserOU          string // e.g. ou=People
	UserFilter      string // e.g. uid=foo
	UserGroupFilter string // e.g. memberUid=foo
	UidFilter       string // e.g. uidNumber=9999
	GidFilter       string // e.g. gidNumber=9999
	TLS             bool   // whether use TLS
}

// search LDAP records
func ldapSearch(username, password string, auth *LDAPAuthenticator) (string, string, []string, error) {
	l, err := ldap.Dial("tcp", auth.AuthURL)
	if err != nil {
		return "", "", nil, err
	}
	defer l.Close()

	// set TLS
	if auth.TLS {
		err = l.StartTLS(&tls.Config{InsecureSkipVerify: true})
		if err != nil {
			return "", "", nil, err
		}
	}
	binddn := fmt.Sprintf("%s=%s,%s,%s", auth.UserFilter, username, auth.UserOU, auth.BaseDN)
	if err = l.Bind(binddn, password); err != nil {
		return "", "", nil, fmt.Errorf("failed to bind: %v", err)
	}
	// first search, get UID
	searchRequest := ldap.NewSearchRequest(
		auth.BaseDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		fmt.Sprintf("(%s=%s)", auth.UserFilter, username), // The filter to apply, e.g. uid=foo
		[]string{fmt.Sprintf("%s", auth.UidFilter)},       // A list attributes to retrieve
		nil,
	)
	sr, err := l.Search(searchRequest)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to search: %v", err)
	}
	uid := ""
	for _, entry := range sr.Entries {
		uid = entry.GetAttributeValue(auth.UidFilter)
	}
	if uid == "" {
		return "", "", nil, fmt.Errorf("failed to find UID")
	}
	// second search, get GIDs
	searchRequest = ldap.NewSearchRequest(
		auth.BaseDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		fmt.Sprintf("(%s=%s)", auth.UserGroupFilter, username), // The filter to apply, e.g. memberUid=foo
		[]string{fmt.Sprintf("%s", auth.GidFilter)},            // A list attributes to retrieve
		nil,
	)
	sr, err = l.Search(searchRequest)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to search: %v", err)
	}
	var gid []string
	for _, entry := range sr.Entries {
		gid = append(gid, entry.GetAttributeValue(auth.GidFilter))
	}

	return username, uid, gid, nil
}

func (ldapAuthenticator *LDAPAuthenticator) AuthenticatePassword(username, password string) (user.Info, bool, error) {
	name, uid, gid, err := ldapSearch(username, password, ldapAuthenticator)
	if err != nil {
		return nil, false, fmt.Errorf("Failed to authenticate with LDAP:%v", err)
	}

	return &user.DefaultInfo{Name: name, UID: uid, Groups: gid}, true, nil
}

// New returns a request authenticator that validates credentials using ldap
func New(ldapConfigFile string) (*LDAPAuthenticator, error) {
	fp, err := os.Open(ldapConfigFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %v", ldapConfigFile, err)
	}
	defer fp.Close()

	decoder := json.NewDecoder(fp)
	var auth LDAPAuthenticator
	if err = decoder.Decode(&auth); err != nil {
		return nil, fmt.Errorf("LDAP: failed to decode %s: err: %v", ldapConfigFile, err)
	}
	if auth.AuthURL == "" {
		return nil, errors.New("LDAP URL is empty")
	}
	if auth.BaseDN == "" {
		return nil, errors.New("LDAP base DN is empty")
	}
	if auth.UserFilter == "" {
		return nil, errors.New("LDAP UserFilter is empty")
	}
	if auth.UserOU == "" {
		return nil, errors.New("LDAP user OU is empty")
	}
	if auth.UserGroupFilter == "" {
		return nil, errors.New("LDAP UserGroupFilter is empty")
	}
	if auth.GidFilter == "" {
		return nil, errors.New("LDAP GidFilter is empty")
	}
	if auth.UidFilter == "" {
		return nil, errors.New("LDAP UidFilter is empty")
	}
	return &auth, nil
}
