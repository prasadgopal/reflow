// Copyright 2017 GRAIL, Inc. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package ec2cluster

import (
	"bytes"
	"testing"
)

func TestCloudconfig(t *testing.T) {
	var c cloudConfig
	c.CoreOS.Update.RebootStrategy = "off"
	c.AppendFile(CloudFile{Path: "/tmp/x", Permissions: "0644", Owner: "root", Content: "a test file"})
	c.AppendUnit(CloudUnit{"reflowlet", "command", true, "unit content"})
	var d cloudConfig
	d.AppendUnit(CloudUnit{"xxx", "xxxcommand", false, "xxx content"})
	d.AppendFile(CloudFile{Path: "/tmp/myfile", Permissions: "0644", Owner: "root", Content: "another test file"})
	c.Merge(&d)
	out, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := out, []byte(`#cloud-config
write_files:
- path: /tmp/x
  permissions: "0644"
  owner: root
  content: a test file
- path: /tmp/myfile
  permissions: "0644"
  owner: root
  content: another test file
coreos:
  update:
    reboot-strategy: "off"
  units:
  - name: reflowlet
    command: command
    enable: true
    content: unit content
  - name: xxx
    command: xxxcommand
    content: xxx content
`); !bytes.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
