package limacharlie

import (
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

func resetResource(org *Organization) {
	orgResources, _ := org.Resources()
	for orgResCat, orgResNames := range orgResources {
		for orgResName := range orgResNames {
			org.ResourceUnsubscribe(orgResName, orgResCat)
		}
	}
}

func TestSyncPushResources(t *testing.T) {
	a := assert.New(t)
	org := getTestOrgFromEnv(a)

	resetResource(org)
	resourcesBase, err := org.Resources()

	a.NoError(err)
	defer resetResource(org)

	resourcesConfig := `
resources:
  api:
    - ip-geo
    - vt
  replicant:
    - exfil
`
	orgConfig := OrgConfig{}
	a.NoError(yaml.Unmarshal([]byte(resourcesConfig), &orgConfig))

	// sync resources in dry run
	ops, err := org.SyncPush(orgConfig, SyncOptions{IsDryRun: true, SyncResources: true})
	a.NoError(err)
	expectedOps := sortSyncOps([]OrgSyncOperation{
		{ElementType: OrgSyncOperationElementType.Resource, ElementName: "api/ip-geo", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.Resource, ElementName: "api/vt", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.Resource, ElementName: "replicant/exfil", IsAdded: true},
	})
	a.Equal(sortSyncOps(expectedOps), sortSyncOps(ops))
	resources, err := org.Resources()
	a.NoError(err)
	a.Equal(resourcesBase, resources)

	// no dry run
	ops, err = org.SyncPush(orgConfig, SyncOptions{SyncResources: true})
	a.NoError(err)
	a.Equal(expectedOps, sortSyncOps(ops))
	resources, err = org.Resources()
	a.NoError(err)
	expectedResources := resourcesBase.duplicate()
	expectedResources.AddToCategory(ResourceCategories.API, "ip-geo")
	expectedResources.AddToCategory(ResourceCategories.API, "vt")
	expectedResources.AddToCategory(ResourceCategories.Replicant, "exfil")
	a.Equal(expectedResources, resources)

	// remove the vt element to test force
	orgConfig.Resources["api"] = []string{"ip-geo"}
	postForce := resources.duplicate()
	postForce.RemoveFromCategory(ResourceCategories.API, "vt")
	// force dry run
	ops, err = org.SyncPush(orgConfig, SyncOptions{IsForce: true, IsDryRun: true, SyncResources: true})
	a.NoError(err)
	expectedOps = sortSyncOps([]OrgSyncOperation{
		{ElementType: OrgSyncOperationElementType.Resource, ElementName: "api/ip-geo"},
		{ElementType: OrgSyncOperationElementType.Resource, ElementName: "api/vt", IsRemoved: true},
		{ElementType: OrgSyncOperationElementType.Resource, ElementName: "replicant/exfil"},
	})
	a.Equal(expectedOps, sortSyncOps(ops))
	a.Equal(expectedResources, resources)

	// no dry run
	ops, err = org.SyncPush(orgConfig, SyncOptions{IsForce: true, SyncResources: true})
	a.NoError(err)
	a.Equal(expectedOps, sortSyncOps(ops))
	resources, err = org.Resources()
	a.NoError(err)
	a.Equal(postForce, resources)

}

func TestSyncPushDRRules(t *testing.T) {
	a := assert.New(t)
	org := getTestOrgFromEnv(a)
	rules, err := org.DRRules()
	a.NoError(err)
	if len(rules) != 0 {
		t.Errorf("unexpected preexisting rules in add/delete: %+v", rules)
	}

	yc := `
rules:
  r1:
    is_enabled: false
    detect:
      op: is
      event: NEW_PROCESS
      path: event/FILE_PATH
      value: nope1
    respond:
      - action: report
        name: t1
  r2:
    is_enabled: true
    detect:
      op: is
      event: NEW_PROCESS
      path: event/FILE_PATH
      value: nope2
    respond:
      - action: report
        name: t2
  r3:
    namespace: managed
    detect:
      op: is
      event: NEW_PROCESS
      path: event/FILE_PATH
      value: nope3
    respond:
      - action: report
        name: t3
`
	c := OrgConfig{}
	err = yaml.Unmarshal([]byte(yc), &c)
	a.NoError(err)

	if len(c.DRRules) != 3 {
		t.Errorf("unexpected conf: %+v", c)
	}

	ops, err := org.SyncPush(c, SyncOptions{
		IsDryRun:    true,
		SyncDRRules: true,
	})
	a.NoError(err)

	if len(ops) != 3 {
		t.Errorf("unexpected ops: %+v", err)
	}
	for _, o := range ops {
		if !o.IsAdded {
			t.Errorf("non-add op: %+v", o)
		}
	}

	rules, err = org.DRRules(WithNamespace("general"))
	a.NoError(err)
	if len(rules) != 0 {
		t.Errorf("general rules is not empty")
	}
	rules, err = org.DRRules(WithNamespace("managed"))
	a.NoError(err)
	if len(rules) != 0 {
		t.Errorf("managed rules is not empty")
	}

	ops, err = org.SyncPush(c, SyncOptions{
		SyncDRRules: true,
	})
	a.NoError(err)

	if len(ops) != 3 {
		t.Errorf("unexpected ops: %+v", err)
	}
	for _, o := range ops {
		if !o.IsAdded {
			t.Errorf("non-add op: %+v", o)
		}
	}
	rules, err = org.DRRules(WithNamespace("general"))
	a.NoError(err)
	if len(rules) != 2 {
		t.Errorf("general rules has: %+v", rules)
	} else {
		if rules["r1"]["is_enabled"].(bool) {
			t.Errorf("rule should be disabled: %+v", rules["r1"])
		}
		if !rules["r2"]["is_enabled"].(bool) {
			t.Errorf("rule should be enabled: %+v", rules["r2"])
		}
	}

	rules, err = org.DRRules(WithNamespace("managed"))
	a.NoError(err)
	if len(rules) != 1 {
		t.Errorf("managed rules has: %+v", rules)
	}

	nc := `
rules:
  r1:
    detect:
      op: is
      event: NEW_PROCESS
      path: event/FILE_PATH
      value: nope1
    respond:
      - action: report
        name: t1
  r2:
    detect:
      op: is
      event: NEW_PROCESS
      path: event/FILE_PATH
      value: nope2
    respond:
      - action: report
        name: t2
  r3:
    namespace: general
    detect:
      op: is
      event: NEW_PROCESS
      path: event/FILE_PATH
      value: nope3
    respond:
      - action: report
        name: t3
`

	c = OrgConfig{}
	err = yaml.Unmarshal([]byte(nc), &c)
	a.NoError(err)

	ops, err = org.SyncPush(c, SyncOptions{
		SyncDRRules: true,
	})
	a.NoError(err)

	if len(ops) != 3 {
		t.Errorf("unexpected ops: %+v", err)
	}
	nNew := 0
	nOld := 0
	for _, o := range ops {
		if o.IsAdded {
			nNew++
		}
		if !o.IsAdded && !o.IsRemoved {
			nOld++
		}
	}
	if nNew != 2 || nOld != 1 {
		t.Errorf("unexpected ops: %v", ops)
	}

	rules, err = org.DRRules(WithNamespace("general"))
	a.NoError(err)
	if len(rules) != 3 {
		t.Errorf("general rules has: %+v", rules)
	}
	rules, err = org.DRRules(WithNamespace("managed"))
	a.NoError(err)
	if len(rules) != 0 {
		t.Errorf("managed rules has: %+v", rules)
	}

	ops, err = org.SyncPush(OrgConfig{}, SyncOptions{
		SyncDRRules: true,
		IsForce:     true,
	})
	a.NoError(err)

	if len(ops) != 3 {
		t.Errorf("unexpected ops: %+v", err)
	}
	for _, o := range ops {
		if !o.IsRemoved {
			t.Errorf("non-remove op: %+v", o)
		}
	}
}

func deleteAllFPRules(org *Organization) {
	rules, _ := org.FPRules()
	for ruleName := range rules {
		org.FPRuleDelete(ruleName)
	}
}

func sortSyncOps(ops []OrgSyncOperation) []OrgSyncOperation {
	sort.Slice(ops, func(i int, j int) bool {
		return ops[i].ElementName < ops[j].ElementName
	})
	return ops
}

func TestSyncPushFPRules(t *testing.T) {
	a := assert.New(t)
	org := getTestOrgFromEnv(a)
	defer deleteAllFPRules(org)

	rules, err := org.FPRules()
	a.NoError(err)
	a.Empty(rules)

	// sync rules in dry run
	orgRules := `
fps:
  fp0:
    data:
      op: ends with
      path: detect/event/FILE_PATH
      value: fp.exe
  fp1:
    data:
      op: is
      path: routing/hostname
      value: google.com
  fp2:
    data:
      op: is
      path: DOMAIN_NAME
      value: 8.8.8.8
`
	orgConfig := OrgConfig{}
	a.NoError(yaml.Unmarshal([]byte(orgRules), &orgConfig))

	ops, err := org.SyncPush(orgConfig, SyncOptions{IsDryRun: true, SyncFPRules: true})
	a.NoError(err)
	expectedOps := sortSyncOps([]OrgSyncOperation{
		{ElementType: OrgSyncOperationElementType.FPRule, ElementName: "fp0", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.FPRule, ElementName: "fp1", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.FPRule, ElementName: "fp2", IsAdded: true},
	})
	a.Equal(expectedOps, sortSyncOps(ops))
	fpRules, err := org.FPRules()
	a.NoError(err)
	a.Empty(fpRules)

	// no dry run
	ops, err = org.SyncPush(orgConfig, SyncOptions{SyncFPRules: true})
	a.NoError(err)
	a.Equal(expectedOps, sortSyncOps(ops))
	fpRules, err = org.FPRules()
	a.NoError(err)
	a.Equal(len(orgConfig.FPRules), len(fpRules))
	for fpRuleName, fpRule := range fpRules {
		configFPRule, found := orgConfig.FPRules[fpRuleName]
		a.True(found)
		a.True(configFPRule.DetectionEquals(fpRule))
	}

	// force sync in dry run
	orgRulesForce := `
fps:
  fp0:
    data:
      op: ends with
      path: detect/event/FILE_PATH
      value: fp.exe
  fp11:
    data:
      op: is
      path: routing/hostname
      value: google.somethingelse
  fp12:
    data:
      op: is
      path: DOMAIN_NAME
      value: 8.8.4.4
`
	orgConfigForce := OrgConfig{}
	a.NoError(yaml.Unmarshal([]byte(orgRulesForce), &orgConfigForce))

	ops, err = org.SyncPush(orgConfigForce, SyncOptions{IsDryRun: true, SyncFPRules: true, IsForce: true})
	a.NoError(err)
	expectedOps = sortSyncOps([]OrgSyncOperation{
		{ElementType: OrgSyncOperationElementType.FPRule, ElementName: "fp0"},
		{ElementType: OrgSyncOperationElementType.FPRule, ElementName: "fp1", IsRemoved: true},
		{ElementType: OrgSyncOperationElementType.FPRule, ElementName: "fp2", IsRemoved: true},
		{ElementType: OrgSyncOperationElementType.FPRule, ElementName: "fp11", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.FPRule, ElementName: "fp12", IsAdded: true},
	})
	a.Equal(expectedOps, sortSyncOps(ops))
	fpRulesForce, err := org.FPRules()
	a.NoError(err)
	for fpRuleName, fpRule := range fpRulesForce {
		configFPRule, found := orgConfig.FPRules[fpRuleName]
		a.True(found)
		a.True(configFPRule.DetectionEquals(fpRule))
	}

	// no dry run
	ops, err = org.SyncPush(orgConfigForce, SyncOptions{SyncFPRules: true, IsForce: true})
	a.NoError(err)
	a.Equal(expectedOps, sortSyncOps(ops))
	fpRulesForce, err = org.FPRules()
	a.NoError(err)
	a.Equal(len(orgConfigForce.FPRules), len(fpRulesForce))
	for fpRuleName, fpRule := range fpRulesForce {
		configFPRule, found := orgConfigForce.FPRules[fpRuleName]
		a.True(found)
		a.True(configFPRule.DetectionEquals(fpRule))
	}
}

func deleteAllOutputs(org *Organization) {
	outputs, _ := org.Outputs()
	for outputName := range outputs {
		org.OutputDel(outputName)
	}
}

func TestSyncPushOutputs(t *testing.T) {
	a := assert.New(t)
	org := getTestOrgFromEnv(a)
	defer deleteAllOutputs(org)

	outputs, err := org.Outputs()
	a.NoError(err)
	a.Empty(outputs)

	yamlOutputs := `
outputs:
  output0:
    module: s3
    type: detect
    bucket: aws-bucket-name
    key_id: 105c750e-8d6f-4ee5-9815-5975fda15e5b
    secret_key: 403aabff-d7a8-4602-ab9c-815a638a8a30
    is_indexing: "true"
    is_compression: "true"
  output1:
    module: scp
    type: artifact
    dest_host: storage.corp.com
    dir: /uploads/
    username: root
    password: 9a7448cb-df59-423d-b879-d3a83d6ced50
  output2:
    module: slack
    type: detect
    slack_api_token: e8ef2263-baeb-4459-87d3-c7d0cff8aba1
    slack_channe: #detections
`
	orgConfig := OrgConfig{}
	a.NoError(yaml.Unmarshal([]byte(yamlOutputs), &orgConfig))

	// sync in dry run
	ops, err := org.SyncPush(orgConfig, SyncOptions{IsDryRun: true, SyncOutputs: true})
	a.NoError(err)
	expectedOps := sortSyncOps([]OrgSyncOperation{
		{ElementType: OrgSyncOperationElementType.Output, ElementName: "output0", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.Output, ElementName: "output1", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.Output, ElementName: "output2", IsAdded: true},
	})
	a.Equal(expectedOps, sortSyncOps(ops))
	outputs, err = org.Outputs()
	a.NoError(err)
	a.Empty(outputs)

	// no dry run
	ops, err = org.SyncPush(orgConfig, SyncOptions{SyncOutputs: true})
	a.NoError(err)
	a.Equal(expectedOps, sortSyncOps(ops))
	outputs, err = org.Outputs()
	a.NoError(err)
	a.Equal(len(orgConfig.Outputs), len(outputs))
	for outputName, output := range outputs {
		configOutput, found := orgConfig.Outputs[outputName]
		a.True(found)
		configOutput.Name = outputName
		a.True(output.Equals(configOutput), "outputs are not equal %v != %v", output, configOutput)
	}

	// force sync in dry run
	yamlOutputs = `
outputs:
  output0:
    module: s3
    type: detect
    bucket: aws-bucket-name
    key_id: 105c750e-8d6f-4ee5-9815-5975fda15e5b
    secret_key: 403aabff-d7a8-4602-ab9c-815a638a8a30
    is_indexing: "true"
    is_compression: "true"
  output11:
    module: scp
    type: artifact
    dest_host: storage.corp.com
    dir: /uploads/
    username: root
    password: 9a7448cb-df59-423d-b879-d3a83d6ced50
  output12:
    module: slack
    type: detect
    slack_api_token: e8ef2263-baeb-4459-87d3-c7d0cff8aba1
    slack_channe: #detections
`
	orgConfigForce := OrgConfig{}
	a.NoError(yaml.Unmarshal([]byte(yamlOutputs), &orgConfigForce))

	ops, err = org.SyncPush(orgConfigForce, SyncOptions{IsDryRun: true, SyncOutputs: true, IsForce: true})
	a.NoError(err)
	expectedOps = sortSyncOps([]OrgSyncOperation{
		{ElementType: OrgSyncOperationElementType.Output, ElementName: "output0"},
		{ElementType: OrgSyncOperationElementType.Output, ElementName: "output1", IsRemoved: true},
		{ElementType: OrgSyncOperationElementType.Output, ElementName: "output2", IsRemoved: true},
		{ElementType: OrgSyncOperationElementType.Output, ElementName: "output11", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.Output, ElementName: "output12", IsAdded: true},
	})
	a.Equal(expectedOps, sortSyncOps(ops))
	outputsForce, err := org.Outputs()
	a.NoError(err)
	for outputName, output := range outputsForce {
		configOutput, found := orgConfig.Outputs[outputName]
		a.True(found)
		configOutput.Name = outputName
		a.True(output.Equals(configOutput), "outputs are not equal %v != %v", output, configOutput)
	}

	// no dry run
	ops, err = org.SyncPush(orgConfigForce, SyncOptions{SyncOutputs: true, IsForce: true})
	a.NoError(err)
	expectedOps = sortSyncOps([]OrgSyncOperation{
		{ElementType: OrgSyncOperationElementType.Output, ElementName: "output0"},
		{ElementType: OrgSyncOperationElementType.Output, ElementName: "output1", IsRemoved: true},
		{ElementType: OrgSyncOperationElementType.Output, ElementName: "output2", IsRemoved: true},
		{ElementType: OrgSyncOperationElementType.Output, ElementName: "output11", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.Output, ElementName: "output12", IsAdded: true},
	})
	a.Equal(expectedOps, sortSyncOps(ops))
	outputsForce, err = org.Outputs()
	a.NoError(err)
	for outputName, output := range outputsForce {
		configOutput, found := orgConfigForce.Outputs[outputName]
		a.True(found)
		configOutput.Name = outputName
		a.True(output.Equals(configOutput), "outputs are not equal %v != %v", output, configOutput)
	}

}

func deleteIntegrityRules(org *Organization) {
	rules, _ := org.IntegrityRules()
	for ruleName := range rules {
		org.IntegrityRuleDelete(ruleName)
	}
}

func TestSyncPushIntegrity(t *testing.T) {
	a := assert.New(t)
	org := getTestOrgFromEnv(a)
	defer deleteIntegrityRules(org)

	unsubReplicantCB, err := findUnsubscribeReplicantCallback(org, "integrity")
	a.NoError(err)
	if unsubReplicantCB != nil {
		defer unsubReplicantCB()
	}

	rules, err := org.IntegrityRules()
	a.NoError(err)
	a.Empty(rules)

	yamlIntegrityRules := `
integrity:
  testrule0:
    patterns:
    - /root/.ssh/authorized_keys
    platforms:
    - linux
  testrule1:
    patterns:
    - /home/user/.ssh/*
    platforms:
    - linux
  testrule2:
    patterns:
    - c:\\test.txt
    platforms:
    - windows
`
	orgConfig := OrgConfig{}
	a.NoError(yaml.Unmarshal([]byte(yamlIntegrityRules), &orgConfig))

	// dry run
	ops, err := org.SyncPush(orgConfig, SyncOptions{IsDryRun: true, SyncIntegrity: true})
	a.NoError(err)
	expectedOps := sortSyncOps([]OrgSyncOperation{
		{ElementType: OrgSyncOperationElementType.Integrity, ElementName: "testrule0", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.Integrity, ElementName: "testrule1", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.Integrity, ElementName: "testrule2", IsAdded: true},
	})
	a.Equal(expectedOps, sortSyncOps(ops))
	rules, err = org.IntegrityRules()
	a.NoError(err)
	a.Empty(rules)

	// no dry run
	ops, err = org.SyncPush(orgConfig, SyncOptions{SyncIntegrity: true})
	a.NoError(err)
	a.Equal(expectedOps, sortSyncOps(ops))
	rules, err = org.IntegrityRules()
	a.NoError(err)
	a.Equal(len(orgConfig.Integrity), len(rules))
	for ruleName, rule := range rules {
		configRule, found := orgConfig.Integrity[ruleName]
		a.True(found)
		a.True(configRule.EqualsContent(rule), "integrity rule content not equal\n%#v\n\n!=\n\n%#v", configRule, rule)
	}

	// force and dry run
	yamlIntegrityRules = `
integrity:
  testrule1:
    patterns:
    - /home/user/.ssh/*
    platforms:
    - linux
  testrule3:
    patterns:
    - /home/user/.gitconfig
    platforms:
    - linux
    - windows
`
	forceOrgConfig := OrgConfig{}
	a.NoError(yaml.Unmarshal([]byte(yamlIntegrityRules), &forceOrgConfig))

	ops, err = org.SyncPush(forceOrgConfig, SyncOptions{IsForce: true, IsDryRun: true, SyncIntegrity: true})
	a.NoError(err)
	expectedOps = sortSyncOps([]OrgSyncOperation{
		{ElementType: OrgSyncOperationElementType.Integrity, ElementName: "testrule1"},
		{ElementType: OrgSyncOperationElementType.Integrity, ElementName: "testrule3", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.Integrity, ElementName: "testrule0", IsRemoved: true},
		{ElementType: OrgSyncOperationElementType.Integrity, ElementName: "testrule2", IsRemoved: true},
	})
	a.Equal(expectedOps, sortSyncOps(ops))
	rules, err = org.IntegrityRules()
	a.NoError(err)
	a.Equal(len(orgConfig.Integrity), len(rules))
	for ruleName, rule := range rules {
		configRule, found := orgConfig.Integrity[ruleName]
		a.True(found, "rule '%s' not found", ruleName)
		a.True(configRule.EqualsContent(rule), "integrity rule content not equal\n%#v\n\n!=\n\n%#v", configRule, rule)
	}

	// force and no dry run

	ops, err = org.SyncPush(forceOrgConfig, SyncOptions{IsForce: true, SyncIntegrity: true})
	a.NoError(err)
	a.Equal(expectedOps, sortSyncOps(ops))
	rules, err = org.IntegrityRules()
	a.NoError(err)
	a.Equal(len(forceOrgConfig.Integrity), len(rules))
	for ruleName, rule := range rules {
		configRule, found := forceOrgConfig.Integrity[ruleName]
		a.True(found, "rule '%s' not found", ruleName)
		a.True(configRule.EqualsContent(rule), "integrity rule content not equal\n%#v\n\n!=\n\n%#v", configRule, rule)
	}
}

func deleteExfil(org *Organization) {
	rules, _ := org.ExfilRules()
	for ruleName := range rules.Watches {
		org.ExfilRuleWatchDelete(ruleName)
	}
	for ruleName := range rules.Events {
		org.ExfilRuleEventDelete(ruleName)
	}
}

func TestSyncPushExfil(t *testing.T) {
	a := assert.New(t)
	org := getTestOrgFromEnv(a)
	defer deleteExfil(org)

	unsubReplicantCB, err := findUnsubscribeReplicantCallback(org, "exfil")
	a.NoError(err)
	if unsubReplicantCB != nil {
		defer unsubReplicantCB()
	}

	rules, err := org.ExfilRules()
	a.NoError(err)
	rulesWatchesLenStart := len(rules.Watches)
	rulesEventsLenStart := len(rules.Events)

	yamlExfil := `
exfil:
  watch:
    watch_evil:
      event: NEW_PROCESS
      path:
        - COMMAND_LINE
      operator: contains
      value: evil
    watch_ps1:
      event: NEW_DOCUMENT
      path:
        - FILE_PATH
      operator: ends with
      value: .ps1
  list:
    event_base:
      events:
        - NEW_PROCESS
        - EXEC_OOB
      filters:
        platforms:
          - windows
          - linux
    event_chrome:
      events:
        - DNS_REQUEST
      filters:
        platforms:
          - chrome
`
	orgConfig := OrgConfig{}
	a.NoError(yaml.Unmarshal([]byte(yamlExfil), &orgConfig))

	// dry run
	ops, err := org.SyncPush(orgConfig, SyncOptions{IsDryRun: true, SyncExfil: true})
	a.NoError(err)
	expectedOps := sortSyncOps([]OrgSyncOperation{
		{ElementType: OrgSyncOperationElementType.ExfilWatch, ElementName: "watch_evil", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.ExfilWatch, ElementName: "watch_ps1", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.ExfilEvent, ElementName: "event_base", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.ExfilEvent, ElementName: "event_chrome", IsAdded: true},
	})
	a.Equal(expectedOps, sortSyncOps(ops))
	rules, err = org.ExfilRules()
	a.NoError(err)
	a.Equal(rulesWatchesLenStart, len(rules.Watches))
	a.Equal(rulesEventsLenStart, len(rules.Events))

	// no dry run
	ops, err = org.SyncPush(orgConfig, SyncOptions{SyncExfil: true})
	a.NoError(err)
	expectedOps = sortSyncOps([]OrgSyncOperation{
		{ElementType: OrgSyncOperationElementType.ExfilWatch, ElementName: "watch_evil", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.ExfilWatch, ElementName: "watch_ps1", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.ExfilEvent, ElementName: "event_base", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.ExfilEvent, ElementName: "event_chrome", IsAdded: true},
	})
	for _, expectedOp := range expectedOps {
		a.Contains(ops, expectedOp)
	}
	rules, err = org.ExfilRules()
	a.NoError(err)

	a.Equal(rulesWatchesLenStart+2, len(rules.Watches))
	for ruleName, watch := range orgConfig.Exfil.Watches {
		configWatch, found := rules.Watches[ruleName]
		a.True(found, "watch '%s' not found", ruleName)
		a.True(watch.EqualsContent(configWatch), "watch content not equals: %v != %v", watch, configWatch)
	}
	rulesWatchesLenStart += 2

	a.Equal(rulesEventsLenStart+2, len(rules.Events))
	for ruleName, event := range orgConfig.Exfil.Events {
		configEvent, found := rules.Events[ruleName]
		a.True(found, "event '%s' not found", ruleName)
		a.True(event.EqualsContent(configEvent), "event content not equals: %v != %v", event, configEvent)
	}
	rulesEventsLenStart += 2

	// force sync and dry run
	yamlExfil = `
exfil:
  watch:
    watch_evil:
      event: NEW_PROCESS
      path:
        - COMMAND_LINE
      operator: contains
      value: evil
  list:
    event_base:
      events:
        - NEW_PROCESS
        - EXEC_OOB
      filters:
        platforms:
          - windows
          - linux
`
	forceOrgConfig := OrgConfig{}
	a.NoError(yaml.Unmarshal([]byte(yamlExfil), &forceOrgConfig))

	ops, err = org.SyncPush(forceOrgConfig, SyncOptions{IsForce: true, IsDryRun: true, SyncExfil: true})
	a.NoError(err)
	expectedOps = sortSyncOps([]OrgSyncOperation{
		{ElementType: OrgSyncOperationElementType.ExfilWatch, ElementName: "watch_evil"},
		{ElementType: OrgSyncOperationElementType.ExfilEvent, ElementName: "event_base"},
		{ElementType: OrgSyncOperationElementType.ExfilWatch, ElementName: "watch_ps1", IsRemoved: true},
		{ElementType: OrgSyncOperationElementType.ExfilEvent, ElementName: "event_chrome", IsRemoved: true},
	})
	for _, expectedOp := range expectedOps {
		a.Contains(ops, expectedOp)
	}
	rules, err = org.ExfilRules()
	a.NoError(err)

	a.Equal(rulesWatchesLenStart, len(rules.Watches))
	for ruleName, watch := range orgConfig.Exfil.Watches {
		configWatch, found := rules.Watches[ruleName]
		a.True(found, "watch '%s' not found", ruleName)
		a.True(watch.EqualsContent(configWatch), "watch content not equals: %v != %v", watch, configWatch)
	}

	a.Equal(rulesEventsLenStart, len(rules.Events))
	for ruleName, event := range orgConfig.Exfil.Events {
		configEvent, found := rules.Events[ruleName]
		a.True(found, "event '%s' not found", ruleName)
		a.True(event.EqualsContent(configEvent), "event content not equals: %v != %v", event, configEvent)
	}

	// no dry run
	ops, err = org.SyncPush(forceOrgConfig, SyncOptions{IsForce: true, SyncExfil: true})
	a.NoError(err)
	for _, expectedOp := range expectedOps {
		a.Contains(ops, expectedOp)
	}
	rules, err = org.ExfilRules()
	a.NoError(err)

	a.Equal(rulesWatchesLenStart-1, len(rules.Watches))
	for ruleName, watch := range forceOrgConfig.Exfil.Watches {
		configWatch, found := rules.Watches[ruleName]
		a.True(found, "watch '%s' not found", ruleName)
		a.True(watch.EqualsContent(configWatch), "watch content not equals: %v != %v", watch, configWatch)
	}
	for ruleName, event := range forceOrgConfig.Exfil.Events {
		configEvent, found := rules.Events[ruleName]
		a.True(found, "event '%s' not found", ruleName)
		a.True(event.EqualsContent(configEvent), "event content not equals: %v != %v", event, configEvent)
	}
}

func deleteArtifacts(org *Organization) {
	rules, _ := org.ArtifactsRules()
	for ruleName := range rules {
		org.ArtifactRuleDelete(ruleName)
	}
}

func TestSyncPushArtifact(t *testing.T) {
	a := assert.New(t)
	org := getTestOrgFromEnv(a)
	defer deleteArtifacts(org)

	unsubCB, err := findUnsubscribeReplicantCallback(org, "logging")
	a.NoError(err)
	if unsubCB != nil {
		defer unsubCB()
	}

	rules, err := org.ArtifactsRules()
	a.NoError(err)
	rulesCountStart := len(rules)

	yamlArtifact := `
artifact:
  linux-logs:
    is_delete_after: false
    is_ignore_cert: false
    patterns:
    - /var/log/syslog.1
    - /var/log/auth.log.1
    platforms:
    - linux
    days_retention: 30
    tags: []
  windows-logs:
    is_delete_after: false
    is_ignore_cert: false
    patterns:
    - c:\\windows\\system32\\winevt\\logs\\Security.evtx
    - c:\\windows\\system32\\winevt\\logs\\System.evtx
    platforms:
    - windows
    days_retention: 30
    tags: []
  browser-chrome-logs:
    is_delete_after: false
    is_ignore_cert: false
    patterns:
    - "%homepath%\\AppData\\Local\\Google\\Chrome\\User Data\\Crashpad\\reports"
    - "~/Library/Application Support/Google/Chrome/Crashpad/completed/"
    platforms:
    - windows
    - macos
    tags: []
`
	orgConfig := OrgConfig{}
	a.NoError(yaml.Unmarshal([]byte(yamlArtifact), &orgConfig))

	// dry run - no force
	ops, err := org.SyncPush(orgConfig, SyncOptions{IsDryRun: true, SyncArtifacts: true})
	a.NoError(err)
	expectedOps := sortSyncOps([]OrgSyncOperation{
		{ElementType: OrgSyncOperationElementType.Artifact, ElementName: "linux-logs", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.Artifact, ElementName: "windows-logs", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.Artifact, ElementName: "browser-chrome-logs", IsAdded: true},
	})
	a.Equal(expectedOps, sortSyncOps(ops))
	rules, err = org.ArtifactsRules()
	a.NoError(err)
	a.Equal(rulesCountStart, len(rules))

	// no force
	ops, err = org.SyncPush(orgConfig, SyncOptions{SyncArtifacts: true})
	a.NoError(err)
	a.Equal(expectedOps, sortSyncOps(ops))

	rules, err = org.ArtifactsRules()
	a.NoError(err)
	a.Equal(rulesCountStart+3, len(rules))
	for ruleName, rule := range orgConfig.Artifacts {
		orgRule, found := rules[ruleName]
		a.True(found, "artifact rule not found for %s", ruleName)
		a.True(rule.EqualsContent(orgRule), "artifact rule content not equal: %v != %v", rule, OrgSyncArtifactRule{}.FromArtifactRule(orgRule))
	}

	// dry run - force
	yamlArtifact = `
artifact:
  windows-logs:
    is_delete_after: false
    is_ignore_cert: false
    patterns:
    - c:\\windows\\system32\\winevt\\logs\\Security.evtx
    - c:\\windows\\system32\\winevt\\logs\\System.evtx
    platforms:
    - windows
    days_retention: 30
    tags: []
`
	forceOrgConfig := OrgConfig{}
	a.NoError(yaml.Unmarshal([]byte(yamlArtifact), &forceOrgConfig))

	ops, err = org.SyncPush(forceOrgConfig, SyncOptions{IsForce: true, IsDryRun: true, SyncArtifacts: true})
	a.NoError(err)
	expectedOps = sortSyncOps([]OrgSyncOperation{
		{ElementType: OrgSyncOperationElementType.Artifact, ElementName: "linux-logs", IsRemoved: true},
		{ElementType: OrgSyncOperationElementType.Artifact, ElementName: "windows-logs"},
		{ElementType: OrgSyncOperationElementType.Artifact, ElementName: "browser-chrome-logs", IsRemoved: true},
	})
	a.Equal(expectedOps, sortSyncOps(ops))
	rules, err = org.ArtifactsRules()
	a.NoError(err)
	a.Equal(rulesCountStart+3, len(rules))
	for ruleName, rule := range orgConfig.Artifacts {
		orgRule, found := rules[ruleName]
		a.True(found, "artifact rule not found for %s", ruleName)
		a.True(rule.EqualsContent(orgRule), "artifact rule content not equal: %v != %v", rule, OrgSyncArtifactRule{}.FromArtifactRule(orgRule))
	}

	// force
	ops, err = org.SyncPush(forceOrgConfig, SyncOptions{IsForce: true, SyncArtifacts: true})
	a.NoError(err)
	a.Equal(expectedOps, sortSyncOps(ops))
	rules, err = org.ArtifactsRules()
	a.NoError(err)
	a.Equal(rulesCountStart+1, len(rules))
	for ruleName, rule := range forceOrgConfig.Artifacts {
		orgRule, found := rules[ruleName]
		a.True(found, "artifact rule not found for %s", ruleName)
		a.True(rule.EqualsContent(orgRule), "artifact rule content not equal: %v != %v", rule, OrgSyncArtifactRule{}.FromArtifactRule(orgRule))
	}
}

func TestMerge(t *testing.T) {
	o1 := OrgConfig{
		Version: 3,
		Resources: orgSyncResources{
			"replicant": []string{
				"a1",
				"a2",
			},
		},
		DRRules: orgSyncDRRules{
			"r1": CoreDRRule{
				Name:      "r1",
				Namespace: "managed",
				Detect: Dict{
					"t": "v",
				},
				Response: List{
					"l1",
					"l2",
				},
			},
			"r2": CoreDRRule{
				Name:      "r2",
				Namespace: "managed",
				Detect: Dict{
					"t": "v",
				},
				Response: List{
					"l1",
					"l2",
				},
			},
		},
	}
	o2 := OrgConfig{
		Resources: orgSyncResources{
			"replicant": []string{
				"a3",
				"a1",
			},
		},
		DRRules: orgSyncDRRules{
			"r1": CoreDRRule{
				Name:      "r1",
				Namespace: "general",
				Detect: Dict{
					"t": "v1",
				},
				Response: List{
					"l11",
					"l21",
				},
			},
		},
	}
	expected := `version: 3
resources:
    replicant:
        - a1
        - a2
        - a3
rules:
    r1:
        name: r1
        namespace: general
        detect:
            t: v1
        respond:
            - l11
            - l21
    r2:
        name: r2
        namespace: managed
        detect:
            t: v
        respond:
            - l1
            - l2
`

	out := o1.Merge(o2)

	yOut, err := yaml.Marshal(out)
	if err != nil {
		t.Errorf("yaml: %v", err)
	}

	if string(yOut) != expected {
		t.Errorf("unexpected config: %s\n!=\n\n%s", string(yOut), expected)
	}
}
func TestPushMultiFiles(t *testing.T) {
	files := map[string][]byte{
		"f1": []byte(`version: 3
resources:
  replicant:
  - a1
  - a2
  - a3
`),
		"r": []byte(`version: 3
include:
- s/f2
- f1
`),
		"s/f2": []byte(`version: 3
include:
- f3
rules:
  r1:
    name: r1
    namespace: managed
    detect:
      t: v1
    respond:
    - l11
    - l21
  r2:
    name: r2
    namespace: managed
    detect:
      t: v
    respond:
    - l1
    - l2
`),
		"s/f3": []byte(`version: 3
rules:
  r1:
    name: r1
    namespace: general
    detect:
      t: v1
    respond:
    - l11
    - l21
`),
	}

	expected := `version: 3
resources:
    replicant:
        - a1
        - a2
        - a3
rules:
    r1:
        name: r1
        namespace: general
        detect:
            t: v1
        respond:
            - l11
            - l21
    r2:
        name: r2
        namespace: managed
        detect:
            t: v
        respond:
            - l1
            - l2
`

	ldr := func(parent string, configFile string) ([]byte, error) {
		full := filepath.Join(filepath.Dir(parent), configFile)
		d, ok := files[full]
		if !ok {
			return nil, fmt.Errorf("file not found: %s", full)
		}
		return d, nil
	}

	out, err := loadEffectiveConfig("", "r", SyncOptions{
		IncludeLoader: ldr,
	})
	if err != nil {
		t.Errorf("failed to load: %v", err)
	}

	yOut, err := yaml.Marshal(out)
	if err != nil {
		t.Errorf("yaml: %v", err)
	} else if string(yOut) != expected {
		t.Errorf("unexpected config: %s\n!=\n\n%s", string(yOut), expected)
	}
}

func TestSyncOrgValues(t *testing.T) {
	a := assert.New(t)
	org := getTestOrgFromEnv(a)

	// Start by zeroing out all values.
	for _, v := range supportedOrgValues {
		err := org.OrgValueSet(v, "")
		a.NoError(err)
	}

	ov1 := uuid.NewString()
	ov2 := uuid.NewString()
	yamlValues := fmt.Sprintf(`org-value:
  otx: %s
  twilio: %s
`, ov1, ov2)
	orgConf := OrgConfig{}
	a.NoError(yaml.Unmarshal([]byte(yamlValues), &orgConf))

	ops, err := org.SyncPush(orgConf, SyncOptions{IsForce: true, SyncOrgValues: true})
	a.NoError(err)
	expectedOps := sortSyncOps([]OrgSyncOperation{
		{ElementType: OrgSyncOperationElementType.OrgValue, ElementName: "otx", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.OrgValue, ElementName: "twilio", IsAdded: true},
	})
	a.Equal(expectedOps, sortSyncOps(ops))
	ov, err := org.OrgValueGet("otx")
	a.NoError(err)
	a.Equal(ov1, ov.Value)
	ov, err = org.OrgValueGet("twilio")
	a.NoError(err)
	a.Equal(ov2, ov.Value)

	yamlValues = fmt.Sprintf(`org-value:
  otx: %s
`, ov1)
	orgConf = OrgConfig{}
	a.NoError(yaml.Unmarshal([]byte(yamlValues), &orgConf))

	ops, err = org.SyncPush(orgConf, SyncOptions{IsForce: true, SyncOrgValues: true})
	a.NoError(err)
	expectedOps = sortSyncOps([]OrgSyncOperation{
		{ElementType: OrgSyncOperationElementType.OrgValue, ElementName: "otx"},
		{ElementType: OrgSyncOperationElementType.OrgValue, ElementName: "twilio", IsRemoved: true},
	})
	a.Equal(expectedOps, sortSyncOps(ops))
	ov, err = org.OrgValueGet("otx")
	a.NoError(err)
	a.Equal(ov1, ov.Value)
	ov, err = org.OrgValueGet("twilio")
	a.NoError(err)
	a.Equal("", ov.Value)
}

func TestSyncFullBidirectional(t *testing.T) {
	rawConf := `version: 3
resources:
    api:
        - vt
        - insight
    replicant:
        - infrastructure-service
        - integrity
        - reliable-tasking
        - responder
        - sigma
        - logging
        - yara
rules:
    vt-domains:
        name: vt-domains
        namespace: general
        detect:
            event: DNS_REQUEST
            metadata_rules:
                length of: true
                op: is greater than
                path: /
                value: 4
            op: lookup
            path: event/DOMAIN_NAME
            resource: lcr://api/vt
        respond:
            - action: report
              name: vt-bad-domain
    vt-hashes:
        name: vt-hashes
        namespace: general
        detect:
            event: CODE_IDENTITY
            metadata_rules:
                length of: true
                op: is greater than
                path: /
                value: 3
            op: lookup
            path: event/HASH
            resource: lcr://api/vt
        respond:
            - action: report
              name: vt-bad-hash
integrity:
    linux-key:
        patterns:
            - /home/*/.ssh/*
        tags: []
        platforms:
            - linux
artifact:
    linux-logs:
        is_ignore_cert: false
        is_delete_after: false
        days_retention: 30
        patterns:
            - /var/log/syslog.1
            - /var/log/auth.log.1
        tags: []
        platforms:
            - linux
    windows-logs:
        is_ignore_cert: false
        is_delete_after: false
        days_retention: 30
        patterns:
            - wel://system:*
            - wel://security:*
            - wel://application:*
        tags: []
        platforms:
            - windows
`
	c := OrgConfig{}
	if err := yaml.Unmarshal([]byte(rawConf), &c); err != nil {
		t.Errorf("failed parsing yaml: %v", err)
	}
	newConf, err := yaml.Marshal(c)
	if err != nil {
		t.Errorf("failed producing yaml: %v", err)
	}
	if string(newConf) != rawConf {
		t.Errorf("round trip through yaml failed to produce same output: %s\n\n!=\n\n%s", newConf, rawConf)
	}
}

func deleteYaraRules(org *Organization) {
	rules, _ := org.IntegrityRules()
	for ruleName := range rules {
		org.IntegrityRuleDelete(ruleName)
	}
}

func TestSyncPushYara(t *testing.T) {
	a := assert.New(t)
	org := getTestOrgFromEnv(a)
	defer deleteYaraRules(org)

	unsubReplicantCB, err := findUnsubscribeReplicantCallback(org, "yara")
	a.NoError(err)
	if unsubReplicantCB != nil {
		defer unsubReplicantCB()
	}

	rules, err := org.YaraListRules()
	a.NoError(err)
	a.Empty(rules)
	sources, err := org.YaraListSources()
	a.NoError(err)
	a.Empty(sources)

	yamlYaraRules := `
yara:
  rules:
    testrule1:
      sources:
        - testsource
      filters:
      tags:
        - t1
      platforms:
        - windows
    testrule2:
      sources:
        - testsource
      filters:
        tags:
          - t2
        platforms:
          - windows
  sources:
    testsource:
      source: https://github.com/Neo23x0/signature-base/blob/master/yara/expl_log4j_cve_2021_44228.yar
`
	orgConfig := OrgConfig{}
	a.NoError(yaml.Unmarshal([]byte(yamlYaraRules), &orgConfig))

	// dry run
	ops, err := org.SyncPush(orgConfig, SyncOptions{IsDryRun: true, SyncYara: true})
	a.NoError(err)
	expectedOps := sortSyncOps([]OrgSyncOperation{
		{ElementType: OrgSyncOperationElementType.YaraSource, ElementName: "testsource", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.YaraRule, ElementName: "testrule1", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.YaraRule, ElementName: "testrule2", IsAdded: true},
	})
	a.Equal(expectedOps, sortSyncOps(ops))
	rules, err = org.YaraListRules()
	a.NoError(err)
	a.Empty(rules)
	sources, err = org.YaraListSources()
	a.NoError(err)
	a.Empty(sources)

	// no dry run
	ops, err = org.SyncPush(orgConfig, SyncOptions{SyncYara: true})
	a.NoError(err)
	a.Equal(expectedOps, sortSyncOps(ops))
	rules, err = org.YaraListRules()
	a.NoError(err)
	a.Equal(len(orgConfig.Yara.Rules), len(rules))
	for ruleName, rule := range rules {
		configRule, found := orgConfig.Yara.Rules[ruleName]
		a.True(found)
		a.True(configRule.EqualsContent(rule), "yara rule content not equal\n%#v\n\n!=\n\n%#v", configRule, rule)
	}
	sources, err = org.YaraListSources()
	a.NoError(err)
	a.Equal(len(orgConfig.Yara.Sources), len(sources))
	for sourceName, source := range sources {
		configRule, found := orgConfig.Yara.Sources[sourceName]
		a.True(found)
		a.True(configRule.EqualsContent(source), "yara source content not equal\n%#v\n\n!=\n\n%#v", configRule, source)
	}

	// force and dry run
	yamlYaraRules = `
yara:
  rules:
    testrule3:
      sources:
        - testsource
      filters:
        tags:
          - t3
        platforms:
          - linux
    testrule2:
      sources:
        - testsource
      filters:
        tags:
          - t2
        platforms:
          - windows
  sources:
    testsource:
      source: https://github.com/Neo23x0/signature-base/blob/master/yara/expl_log4j_cve_2021_44228.yar
`
	forceOrgConfig := OrgConfig{}
	a.NoError(yaml.Unmarshal([]byte(yamlYaraRules), &forceOrgConfig))

	ops, err = org.SyncPush(forceOrgConfig, SyncOptions{IsForce: true, IsDryRun: true, SyncYara: true})
	a.NoError(err)
	expectedOps = sortSyncOps([]OrgSyncOperation{
		{ElementType: OrgSyncOperationElementType.YaraSource, ElementName: "testsource"},
		{ElementType: OrgSyncOperationElementType.YaraRule, ElementName: "testrule3", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.YaraRule, ElementName: "testrule2"},
		{ElementType: OrgSyncOperationElementType.YaraRule, ElementName: "testrule1", IsRemoved: true},
	})
	a.Equal(expectedOps, sortSyncOps(ops))
	rules, err = org.YaraListRules()
	a.NoError(err)
	a.Equal(len(orgConfig.Yara.Rules), len(rules))
	for ruleName, rule := range rules {
		configRule, found := orgConfig.Yara.Rules[ruleName]
		a.True(found)
		a.True(configRule.EqualsContent(rule), "yara rule content not equal\n%#v\n\n!=\n\n%#v", configRule, rule)
	}
	sources, err = org.YaraListSources()
	a.NoError(err)
	a.Equal(len(orgConfig.Yara.Sources), len(sources))
	for sourceName, source := range sources {
		configRule, found := orgConfig.Yara.Sources[sourceName]
		a.True(found)
		a.True(configRule.EqualsContent(source), "yara source content not equal\n%#v\n\n!=\n\n%#v", configRule, source)
	}

	// force and no dry run

	ops, err = org.SyncPush(forceOrgConfig, SyncOptions{IsForce: true, SyncYara: true})
	a.NoError(err)
	a.Equal(expectedOps, sortSyncOps(ops))
	rules, err = org.YaraListRules()
	a.NoError(err)
	a.Equal(len(forceOrgConfig.Yara.Rules), len(rules))
	for ruleName, rule := range rules {
		configRule, found := forceOrgConfig.Yara.Rules[ruleName]
		a.True(found)
		a.True(configRule.EqualsContent(rule), "yara rule content not equal\n%#v\n\n!=\n\n%#v", configRule, rule)
	}
	sources, err = org.YaraListSources()
	a.NoError(err)
	a.Equal(len(forceOrgConfig.Yara.Sources), len(sources))
	for sourceName, source := range sources {
		configRule, found := forceOrgConfig.Yara.Sources[sourceName]
		a.True(found)
		a.True(configRule.EqualsContent(source), "yara source content not equal\n%#v\n\n!=\n\n%#v", configRule, source)
	}
}

func TestSyncInstallationKeys(t *testing.T) {
	a := assert.New(t)
	org := getTestOrgFromEnv(a)
	deleteAllInstallationKeys(org)
	defer deleteAllInstallationKeys(org)

	keys, err := org.InstallationKeys()
	a.NoError(err)
	a.Empty(keys)

	// sync rules in dry run
	orgKeys := `
installation_keys:
  testk1:
    desc: testk1
    tags:
      - t1
      - t2
  testk2:
    desc: testk2
    tags:
      - t1
      - t2
  testk3:
    desc: testk3
    tags:
      - t1
      - t2
`
	orgConfig := OrgConfig{}
	a.NoError(yaml.Unmarshal([]byte(orgKeys), &orgConfig))

	ops, err := org.SyncPush(orgConfig, SyncOptions{IsDryRun: true, SyncInstallationKeys: true})
	a.NoError(err)
	expectedOps := sortSyncOps([]OrgSyncOperation{
		{ElementType: OrgSyncOperationElementType.InstallationKey, ElementName: "testk1", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.InstallationKey, ElementName: "testk2", IsAdded: true},
		{ElementType: OrgSyncOperationElementType.InstallationKey, ElementName: "testk3", IsAdded: true},
	})
	a.Equal(expectedOps, sortSyncOps(ops))
	keys, err = org.InstallationKeys()
	a.NoError(err)
	a.Empty(keys)

	// no dry run
	ops, err = org.SyncPush(orgConfig, SyncOptions{SyncInstallationKeys: true})
	a.NoError(err)
	a.Equal(expectedOps, sortSyncOps(ops))
	keys, err = org.InstallationKeys()
	a.NoError(err)
	a.Equal(len(orgConfig.InstallationKeys), len(keys))
	for _, k := range keys {
		configKey, found := orgConfig.InstallationKeys[k.Description]
		a.True(found)
		a.True(configKey.EqualsContent(k))
	}

	// force sync in dry run
	orgKeysForce := `
installation_keys:
  testk1:
    desc: testk1
    tags:
      - t1
      - t2
  testk4:
    desc: testk4
    tags:
      - t1
  testk3:
    desc: testk3
    tags:
      - t1
      - t2
`
	orgConfigForce := OrgConfig{}
	a.NoError(yaml.Unmarshal([]byte(orgKeysForce), &orgConfigForce))

	ops, err = org.SyncPush(orgConfigForce, SyncOptions{IsDryRun: true, SyncInstallationKeys: true, IsForce: true})
	a.NoError(err)
	expectedOps = sortSyncOps([]OrgSyncOperation{
		{ElementType: OrgSyncOperationElementType.InstallationKey, ElementName: "testk1"},
		{ElementType: OrgSyncOperationElementType.InstallationKey, ElementName: "testk3"},
		{ElementType: OrgSyncOperationElementType.InstallationKey, ElementName: "testk2", IsRemoved: true},
		{ElementType: OrgSyncOperationElementType.InstallationKey, ElementName: "testk4", IsAdded: true},
	})
	a.Equal(expectedOps, sortSyncOps(ops))
	keysForce, err := org.InstallationKeys()
	a.NoError(err)
	for _, k := range keysForce {
		configKey, found := orgConfig.InstallationKeys[k.Description]
		a.True(found)
		a.True(configKey.EqualsContent(k))
	}

	// no dry run
	ops, err = org.SyncPush(orgConfigForce, SyncOptions{SyncInstallationKeys: true, IsForce: true})
	a.NoError(err)
	a.Equal(expectedOps, sortSyncOps(ops))
	keysForce, err = org.InstallationKeys()
	a.NoError(err)
	a.Equal(len(orgConfigForce.InstallationKeys), len(keysForce))
	for _, k := range keysForce {
		configKey, found := orgConfigForce.InstallationKeys[k.Description]
		a.True(found)
		a.True(configKey.EqualsContent(k))
	}
}

func deleteAllInstallationKeys(org *Organization) {
	keys, _ := org.InstallationKeys()
	for _, k := range keys {
		org.DelInstallationKey(k.ID)
	}
	time.Sleep(1 * time.Second)
}
