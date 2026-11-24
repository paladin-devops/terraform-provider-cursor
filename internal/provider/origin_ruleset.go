package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const (
	originRulesetKindMergeBranch    = "merge_branch"
	originRulesetKindPushBranch     = "push_branch"
	originRulesetKindPushTag        = "push_tag"
	originRulesetKindPushRepository = "push_repository"

	originRulesetEnforcementActive   = "active"
	originRulesetEnforcementEvaluate = "evaluate"
	originRulesetEnforcementDisabled = "disabled"

	originBypassModeAlways          = "always"
	originBypassModePullRequestOnly = "pull_request_only"

	originRoleNamespaceAdmin  = "namespace_admin"
	originRoleRepositoryAdmin = "repository_admin"
	originRoleRepositoryWrite = "repository_write"

	maxOriginIncludedRefs = 64
	// Create Ruleset and Update Ruleset accept 50 rules and 100 bypass actors.
	maxOriginRules        = 50
	maxOriginBypassActors = 100

	// originBypassPrincipalChoices is the set of bypass_actor blocks a
	// configuration may set. user, group, and app are the principals the
	// Origin API stores. team and origin_role remain in the schema so
	// existing configurations still parse; see the deprecation messages.
	originBypassPrincipalChoices = "user, group, app, team, or origin_role"

	// originBypassTeamDeprecation documents the October 17, 2026 replacement
	// of bypassActors[].team with group. The provider still accepts team and
	// sends group_public_id as group.id so an existing bypass keeps working.
	originBypassTeamDeprecation = "The Origin API no longer accepts a team bypass actor. Replace team { group_public_id = \"grp_...\" } with group { id = \"grp_...\" } and remove organization_public_id. Origin derives the organization from the group id. Until you migrate, this provider sends group_public_id as group.id and does not send organization_public_id."

	// originBypassOriginRoleDeprecation documents the October 16, 2026 removal
	// of bypassActors[].originRole. The API rejects the write, and a role
	// actor never granted a bypass. The block stays in the schema so existing
	// configurations get this migration error instead of an unknown-block error.
	originBypassOriginRoleDeprecation = "The Origin API no longer accepts an origin_role bypass actor and rejects the ruleset write. A role actor never granted a bypass, because Origin does not resolve who holds a role. Remove this block. To bypass the ruleset, add user, group, or app actors for the principals who should bypass it."
)

type originRuleset struct {
	ID               string                     `json:"id"`
	Name             string                     `json:"name"`
	Description      string                     `json:"description"`
	Enforcement      string                     `json:"enforcement"`
	Kind             string                     `json:"kind"`
	IncludedRefNames []string                   `json:"includedRefNames"`
	ExcludedRefNames []string                   `json:"excludedRefNames"`
	Rules            []originRulesetRule        `json:"rules"`
	BypassActors     []originRulesetBypassActor `json:"bypassActors"`
}

type originRulesetWrite struct {
	Name             string                          `json:"name"`
	Description      string                          `json:"description"`
	Enforcement      string                          `json:"enforcement"`
	Kind             string                          `json:"kind"`
	IncludedRefNames []string                        `json:"includedRefNames"`
	ExcludedRefNames []string                        `json:"excludedRefNames"`
	Rules            []originRulesetRuleInput        `json:"rules"`
	BypassActors     []originRulesetBypassActorInput `json:"bypassActors"`
}

type originRulesetRule struct {
	ID         string          `json:"id"`
	RuleType   string          `json:"ruleType"`
	Parameters json.RawMessage `json:"parameters,omitempty"`
}

type originRulesetRuleInput struct {
	RuleType   string          `json:"ruleType"`
	Parameters json.RawMessage `json:"parameters,omitempty"`
}

type originRulesetBypassActor struct {
	ID         string                    `json:"id"`
	BypassMode string                    `json:"bypassMode"`
	User       *originRulesetUserBypass  `json:"user,omitempty"`
	Group      *originRulesetGroupBypass `json:"group,omitempty"`
	App        *originRulesetAppBypass   `json:"app,omitempty"`
	// Team and OriginRole are not accepted on write. They stay on the read
	// struct so a response that still carries one can be decoded instead of
	// failing the refresh.
	Team       *originRulesetTeamBypass       `json:"team,omitempty"`
	OriginRole *originRulesetOriginRoleBypass `json:"originRole,omitempty"`
}

type originRulesetBypassActorInput struct {
	BypassMode string                    `json:"bypassMode"`
	User       *originRulesetUserBypass  `json:"user,omitempty"`
	Group      *originRulesetGroupBypass `json:"group,omitempty"`
	App        *originRulesetAppBypass   `json:"app,omitempty"`
}

type originRulesetUserBypass struct {
	ID string `json:"id"`
}

type originRulesetGroupBypass struct {
	ID string `json:"id"`
}

type originRulesetTeamBypass struct {
	OrganizationPublicID string `json:"organizationPublicId"`
	GroupPublicID        string `json:"groupPublicId"`
}

type originRulesetAppBypass struct {
	ID string `json:"id"`
}

type originRulesetOriginRoleBypass struct {
	Role string `json:"role"`
}

func (c *apiClient) createOriginRuleset(ctx context.Context, owner, repo string, body originRulesetWrite) (*originRuleset, error) {
	payload, err := c.originDo(ctx, http.MethodPost, originRulesetsPath(owner, repo), body, http.StatusOK, http.StatusCreated)
	if err != nil {
		return nil, err
	}
	return decodeOriginRuleset(payload)
}

func (c *apiClient) getOriginRuleset(ctx context.Context, owner, repo, rulesetID string) (*originRuleset, error) {
	if err := requireRulesetID(rulesetID); err != nil {
		return nil, err
	}
	payload, err := c.originDo(ctx, http.MethodGet, originRulesetPath(owner, repo, rulesetID), nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	return decodeOriginRuleset(payload)
}

func (c *apiClient) updateOriginRuleset(ctx context.Context, owner, repo, rulesetID string, body originRulesetWrite) (*originRuleset, error) {
	if err := requireRulesetID(rulesetID); err != nil {
		return nil, err
	}
	payload, err := c.originDo(ctx, http.MethodPut, originRulesetPath(owner, repo, rulesetID), body, http.StatusOK)
	if err != nil {
		return nil, err
	}
	return decodeOriginRuleset(payload)
}

func (c *apiClient) deleteOriginRuleset(ctx context.Context, owner, repo, rulesetID string) error {
	if err := requireRulesetID(rulesetID); err != nil {
		return err
	}
	_, err := c.originDo(ctx, http.MethodDelete, originRulesetPath(owner, repo, rulesetID), nil, http.StatusOK, http.StatusNoContent)
	return err
}

func requireRulesetID(rulesetID string) error {
	if rulesetID == "" {
		return fmt.Errorf("ruleset id is required")
	}
	if strings.TrimSpace(rulesetID) != rulesetID {
		return fmt.Errorf("ruleset id must not have leading or trailing spaces")
	}
	return nil
}

func originRulesetsPath(owner, repo string) string {
	return originRepoPath(owner, repo) + "/rulesets"
}

func originRulesetPath(owner, repo, rulesetID string) string {
	return originRulesetsPath(owner, repo) + "/" + url.PathEscape(rulesetID)
}

func decodeOriginRuleset(body []byte) (*originRuleset, error) {
	var ruleset originRuleset
	if err := json.Unmarshal(body, &ruleset); err != nil {
		return nil, fmt.Errorf("decoding Origin ruleset: %w", err)
	}
	if ruleset.ID == "" {
		return nil, fmt.Errorf("Origin API returned a ruleset without an id")
	}
	if ruleset.Name == "" {
		return nil, fmt.Errorf("Origin API returned a ruleset without a name")
	}
	return &ruleset, nil
}
