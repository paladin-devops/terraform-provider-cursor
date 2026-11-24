package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Origin rule types. Each one maps to exactly one typed block under rule.
// Merge rules are evaluated when a pull request merges (kind merge_branch);
// push rules are evaluated on ref updates (kind push_branch, push_tag,
// push_repository). max_file_size runs only for push_repository. The wire
// format is ruleType plus a JSON parameters object whose keys are the
// camelCase form of the block attributes.
const (
	originRuleTypePullRequest           = "pull_request"
	originRuleTypeRequireStatusChecks   = "require_status_checks"
	originRuleTypeRequireBranchUpToDate = "require_branch_up_to_date"
	originRuleTypeDeletion              = "deletion"
	originRuleTypeNonFastForward        = "non_fast_forward"
	originRuleTypeBlockDirectUpdates    = "block_direct_updates"
	originRuleTypeBlockMerges           = "block_merges"
	originRuleTypeRequiredLinearHistory = "required_linear_history"
	originRuleTypeRefNamePattern        = "ref_name_pattern"
	originRuleTypeMaxFileSize           = "max_file_size"

	maxOriginRequiredApprovingReviews = 50
	// requiredReviewers holds at most 20 groups. minimumApprovals is 1..50.
	maxOriginRequiredReviewers       = 20
	maxOriginReviewerApprovals       = 50
	originMergeMethodMerge           = "merge"
	originMergeMethodSquash          = "squash"
	maxOriginFileSizeBytes     int64 = 104857600
)

type originRuleCategory int

const (
	originMergeRule originRuleCategory = iota
	originPushRule
)

type originRuleTypeSpec struct {
	ruleType string
	category originRuleCategory
	// kinds, when set, restricts the rule to those ruleset kinds. Empty means
	// every kind in category.
	kinds []string
}

// originRuleTypeSpecs is the ordered registry of typed rule blocks. The order
// is used for error messages and documentation only.
var originRuleTypeSpecs = []originRuleTypeSpec{
	{ruleType: originRuleTypePullRequest, category: originMergeRule},
	{ruleType: originRuleTypeRequireStatusChecks, category: originMergeRule},
	{ruleType: originRuleTypeRequireBranchUpToDate, category: originMergeRule},
	{ruleType: originRuleTypeDeletion, category: originPushRule},
	{ruleType: originRuleTypeNonFastForward, category: originPushRule},
	{ruleType: originRuleTypeBlockDirectUpdates, category: originPushRule},
	{ruleType: originRuleTypeBlockMerges, category: originPushRule},
	{ruleType: originRuleTypeRequiredLinearHistory, category: originPushRule},
	{ruleType: originRuleTypeRefNamePattern, category: originPushRule},
	{ruleType: originRuleTypeMaxFileSize, category: originPushRule, kinds: []string{originRulesetKindPushRepository}},
}

func originRuleTypeNames() []string {
	names := make([]string, 0, len(originRuleTypeSpecs))
	for _, spec := range originRuleTypeSpecs {
		names = append(names, spec.ruleType)
	}
	return names
}

func originRuleSpec(ruleType string) (originRuleTypeSpec, bool) {
	for _, spec := range originRuleTypeSpecs {
		if spec.ruleType == ruleType {
			return spec, true
		}
	}
	return originRuleTypeSpec{}, false
}

// originRuleAllowedOnKind reports whether ruleType runs in a ruleset of kind.
func originRuleAllowedOnKind(ruleType, kind string) bool {
	spec, ok := originRuleSpec(ruleType)
	if !ok {
		return false
	}
	if len(spec.kinds) > 0 {
		for _, allowed := range spec.kinds {
			if kind == allowed {
				return true
			}
		}
		return false
	}
	kindCategory, known := originKindCategory(kind)
	return known && kindCategory == spec.category
}

func originRuleKindError(name, ruleType string) error {
	spec, ok := originRuleSpec(ruleType)
	if !ok {
		return fmt.Errorf("%s has unknown rule type %s", name, ruleType)
	}
	if len(spec.kinds) == 1 {
		return fmt.Errorf("%s.%s is only valid when kind is %s", name, ruleType, spec.kinds[0])
	}
	if len(spec.kinds) > 1 {
		return fmt.Errorf("%s.%s is only valid when kind is %s", name, ruleType, strings.Join(spec.kinds, ", "))
	}
	if spec.category == originMergeRule {
		return fmt.Errorf("%s.%s is a merge rule and requires kind %s", name, ruleType, originRulesetKindMergeBranch)
	}
	return fmt.Errorf("%s.%s is a push rule and requires kind %s, %s, or %s", name, ruleType, originRulesetKindPushBranch, originRulesetKindPushTag, originRulesetKindPushRepository)
}

func originKindCategory(kind string) (originRuleCategory, bool) {
	switch kind {
	case originRulesetKindMergeBranch:
		return originMergeRule, true
	case originRulesetKindPushBranch, originRulesetKindPushTag, originRulesetKindPushRepository:
		return originPushRule, true
	default:
		return 0, false
	}
}

// originRepoRulesetRuleModel is one rule block. Exactly one typed block is set.
type originRepoRulesetRuleModel struct {
	ID                    types.String                        `tfsdk:"id"`
	PullRequest           *originPullRequestRuleModel         `tfsdk:"pull_request"`
	RequireStatusChecks   *originRequireStatusChecksRuleModel `tfsdk:"require_status_checks"`
	RequireBranchUpToDate *originEmptyRuleModel               `tfsdk:"require_branch_up_to_date"`
	Deletion              *originEmptyRuleModel               `tfsdk:"deletion"`
	NonFastForward        *originEmptyRuleModel               `tfsdk:"non_fast_forward"`
	BlockDirectUpdates    *originEmptyRuleModel               `tfsdk:"block_direct_updates"`
	BlockMerges           *originEmptyRuleModel               `tfsdk:"block_merges"`
	RequiredLinearHistory *originEmptyRuleModel               `tfsdk:"required_linear_history"`
	RefNamePattern        *originRefNamePatternRuleModel      `tfsdk:"ref_name_pattern"`
	MaxFileSize           *originMaxFileSizeRuleModel         `tfsdk:"max_file_size"`
}

type originPullRequestRuleModel struct {
	RequiredApprovingReviewCount   types.Int64                   `tfsdk:"required_approving_review_count"`
	DismissStaleReviewsOnPush      types.Bool                    `tfsdk:"dismiss_stale_reviews_on_push"`
	RequireCodeOwnerReview         types.Bool                    `tfsdk:"require_code_owner_review"`
	RequireLastPushApproval        types.Bool                    `tfsdk:"require_last_push_approval"`
	RequiredReviewThreadResolution types.Bool                    `tfsdk:"required_review_thread_resolution"`
	AllowedMergeMethods            types.List                    `tfsdk:"allowed_merge_methods"`
	RequiredReviewers              []originRequiredReviewerModel `tfsdk:"required_reviewer"`
}

type originRequiredReviewerModel struct {
	Group            *originRequiredReviewerGroupModel `tfsdk:"group"`
	MinimumApprovals types.Int64                       `tfsdk:"minimum_approvals"`
}

type originRequiredReviewerGroupModel struct {
	ID types.String `tfsdk:"id"`
}

type originMaxFileSizeRuleModel struct {
	MaxFileSizeBytes types.Int64 `tfsdk:"max_file_size_bytes"`
}

type originRequireStatusChecksRuleModel struct {
	RequiredChecks []originRequiredCheckModel `tfsdk:"required_check"`
}

type originRequiredCheckModel struct {
	ActorKind types.String `tfsdk:"actor_kind"`
	ActorID   types.String `tfsdk:"actor_id"`
	GroupKey  types.String `tfsdk:"group_key"`
	RunKey    types.String `tfsdk:"run_key"`
	Name      types.String `tfsdk:"name"`
}

type originRefNamePatternRuleModel struct {
	Pattern types.String `tfsdk:"pattern"`
	Negate  types.Bool   `tfsdk:"negate"`
}

// originEmptyRuleModel is the body of a rule that takes no parameters, for
// example `deletion {}`.
type originEmptyRuleModel struct{}

// Wire shapes for the parameters object, keyed in camelCase.
type originPullRequestParameters struct {
	RequiredApprovingReviewCount   *int64                             `json:"requiredApprovingReviewCount,omitempty"`
	DismissStaleReviewsOnPush      *bool                              `json:"dismissStaleReviewsOnPush,omitempty"`
	RequireCodeOwnerReview         *bool                              `json:"requireCodeOwnerReview,omitempty"`
	RequireLastPushApproval        *bool                              `json:"requireLastPushApproval,omitempty"`
	RequiredReviewThreadResolution *bool                              `json:"requiredReviewThreadResolution,omitempty"`
	AllowedMergeMethods            []string                           `json:"allowedMergeMethods,omitempty"`
	RequiredReviewers              []originRequiredReviewerParameters `json:"requiredReviewers,omitempty"`
}

type originRequiredReviewerParameters struct {
	Group            originRequiredReviewerGroupParameters `json:"group"`
	MinimumApprovals *int64                                `json:"minimumApprovals,omitempty"`
}

type originRequiredReviewerGroupParameters struct {
	ID string `json:"id"`
}

type originMaxFileSizeParameters struct {
	MaxFileSizeBytes *int64 `json:"maxFileSizeBytes,omitempty"`
}

func (p originPullRequestParameters) empty() bool {
	return p.RequiredApprovingReviewCount == nil && p.DismissStaleReviewsOnPush == nil && p.RequireCodeOwnerReview == nil && p.RequireLastPushApproval == nil && p.RequiredReviewThreadResolution == nil && len(p.AllowedMergeMethods) == 0 && len(p.RequiredReviewers) == 0
}

type originRequireStatusChecksParameters struct {
	RequiredChecks []originRequiredCheckParameters `json:"requiredChecks"`
}

type originRequiredCheckParameters struct {
	ActorKind string `json:"actorKind"`
	ActorID   string `json:"actorId"`
	GroupKey  string `json:"groupKey"`
	RunKey    string `json:"runKey,omitempty"`
	Name      string `json:"name,omitempty"`
}

type originRefNamePatternParameters struct {
	Pattern string `json:"pattern"`
	Negate  *bool  `json:"negate,omitempty"`
}

func originRuleBlocks() map[string]schema.Block {
	return map[string]schema.Block{
		originRuleTypePullRequest: schema.SingleNestedBlock{
			Description: "Merge rule: require a pull request before merging. Every argument is optional; omitted arguments use the Origin default. allowed_merge_methods and required_reviewer are sent on update when set, so an update does not clear them. Rulesets of kind merge_branch only.",
			Attributes: map[string]schema.Attribute{
				"required_approving_review_count": schema.Int64Attribute{
					Optional:    true,
					Description: "Number of approving reviews required before the pull request can merge, from 0 to 50. An approval counts only while the user can submit reviews on the repository.",
				},
				"dismiss_stale_reviews_on_push": schema.BoolAttribute{
					Optional:    true,
					Description: "Dismiss existing approvals when new commits are pushed to the pull request.",
				},
				"require_code_owner_review": schema.BoolAttribute{
					Optional:    true,
					Description: "Require an approving review from a code owner of every changed file.",
				},
				"require_last_push_approval": schema.BoolAttribute{
					Optional:    true,
					Description: "Require the most recent push to be approved by someone other than the person who pushed it.",
				},
				"required_review_thread_resolution": schema.BoolAttribute{
					Optional:    true,
					Description: "Require every review thread to be resolved before merging.",
				},
				"allowed_merge_methods": schema.ListAttribute{
					Optional:    true,
					ElementType: types.StringType,
					Description: "Merge methods a pull request can land with on a targeted branch: merge, squash, or both. Omit it to allow every method the repository allows. An empty list is rejected. A method the repository disables stays disabled, and when several active rulesets apply, only a method every one of them lists is allowed.",
				},
			},
			Blocks: map[string]schema.Block{
				"required_reviewer": schema.ListNestedBlock{
					Description: fmt.Sprintf("Cursor organization group whose members must approve, in addition to required_approving_review_count. At most %d groups, each listed once. A group that later stops resolving blocks every merge this rule covers. An approval counts only while the member can submit reviews on the repository.", maxOriginRequiredReviewers),
					NestedObject: schema.NestedBlockObject{
						Attributes: map[string]schema.Attribute{
							"minimum_approvals": schema.Int64Attribute{
								Optional:    true,
								Description: fmt.Sprintf("Approvals required from distinct members of the group, from 1 to %d. Omit it to use the Origin default of 1.", maxOriginReviewerApprovals),
							},
						},
						Blocks: map[string]schema.Block{
							"group": schema.SingleNestedBlock{
								Description: "The group that must approve, named by its immutable public ID. Required in each required_reviewer.",
								Attributes: map[string]schema.Attribute{
									"id": schema.StringAttribute{
										Optional:    true,
										Description: "Group public ID (grp_...). Origin derives the group's organization from it. Required when the group block is set.",
									},
								},
							},
						},
					},
				},
			},
		},
		originRuleTypeRequireStatusChecks: schema.SingleNestedBlock{
			Description: "Merge rule: require the listed checks to pass on the head commit before merging. Rulesets of kind merge_branch only.",
			Blocks: map[string]schema.Block{
				"required_check": schema.ListNestedBlock{
					Description: "A check that must pass, identified by the actor that reports it and its group key. Repeat the block for each required check.",
					NestedObject: schema.NestedBlockObject{
						Attributes: map[string]schema.Attribute{
							"actor_kind": schema.StringAttribute{
								Required:    true,
								Description: "Kind of actor that reports the check, for example app.",
							},
							"actor_id": schema.StringAttribute{
								Required:    true,
								Description: "ID of the actor that reports the check, for example an Origin app ID (app_...).",
							},
							"group_key": schema.StringAttribute{
								Required:    true,
								Description: "Check group key the actor reports under.",
							},
							"run_key": schema.StringAttribute{
								Optional:    true,
								Description: "Specific check run key within the group. Omit it to require the group as a whole.",
							},
							"name": schema.StringAttribute{
								Optional:    true,
								Description: "Display name of the required check.",
							},
						},
					},
				},
			},
		},
		originRuleTypeRequireBranchUpToDate: schema.SingleNestedBlock{
			Description: "Merge rule: require the pull request head to be up to date with the base branch before merging. Takes no arguments; write `require_branch_up_to_date {}`. Rulesets of kind merge_branch only.",
		},
		originRuleTypeDeletion: schema.SingleNestedBlock{
			Description: "Push rule: block deleting matching refs. Takes no arguments; write `deletion {}`. Rulesets of kind push_branch, push_tag, or push_repository only.",
		},
		originRuleTypeNonFastForward: schema.SingleNestedBlock{
			Description: "Push rule: block force pushes to matching refs. Takes no arguments; write `non_fast_forward {}`. Rulesets of kind push_branch, push_tag, or push_repository only.",
		},
		originRuleTypeBlockDirectUpdates: schema.SingleNestedBlock{
			Description: "Push rule: require updates to matching refs to go through a pull request. Takes no arguments; write `block_direct_updates {}`, which stores the rule with Origin's default of blocking direct updates. Rulesets of kind push_branch, push_tag, or push_repository only.",
		},
		originRuleTypeBlockMerges: schema.SingleNestedBlock{
			Description: "Push rule: block pull request merges into matching refs. Takes no arguments; write `block_merges {}`. Rulesets of kind push_branch, push_tag, or push_repository only.",
		},
		originRuleTypeRequiredLinearHistory: schema.SingleNestedBlock{
			Description: "Push rule: block merge commits so matching refs keep a linear history. Takes no arguments; write `required_linear_history {}`. Rulesets of kind push_branch, push_tag, or push_repository only.",
		},
		originRuleTypeRefNamePattern: schema.SingleNestedBlock{
			Description: "Push rule: restrict ref names that can be created or updated. Rulesets of kind push_branch, push_tag, or push_repository only.",
			Attributes: map[string]schema.Attribute{
				"pattern": schema.StringAttribute{
					Optional:    true,
					Description: "Pattern the ref name is matched against. Required when the ref_name_pattern block is set.",
				},
				"negate": schema.BoolAttribute{
					Optional:    true,
					Description: "When true, ref names that match the pattern are rejected instead of required. Omit it to use the Origin default.",
				},
			},
		},
		originRuleTypeMaxFileSize: schema.SingleNestedBlock{
			Description: fmt.Sprintf("Push rule: reject a push that introduces a file larger than max_file_size_bytes. The whole push is rejected, including other refs it changes. Files the repository already stores are not judged again. Only rulesets of kind %s accept this rule.", originRulesetKindPushRepository),
			Attributes: map[string]schema.Attribute{
				"max_file_size_bytes": schema.Int64Attribute{
					Optional:    true,
					Description: fmt.Sprintf("Largest file a push can introduce, in bytes, from 1 to %d (100 MiB). Required when the max_file_size block is set.", maxOriginFileSizeBytes),
				},
			},
		},
	}
}

// typedBlocks returns the names of the typed blocks set on this rule.
func (m originRepoRulesetRuleModel) typedBlocks() []string {
	var set []string
	if m.PullRequest != nil {
		set = append(set, originRuleTypePullRequest)
	}
	if m.RequireStatusChecks != nil {
		set = append(set, originRuleTypeRequireStatusChecks)
	}
	if m.RequireBranchUpToDate != nil {
		set = append(set, originRuleTypeRequireBranchUpToDate)
	}
	if m.Deletion != nil {
		set = append(set, originRuleTypeDeletion)
	}
	if m.NonFastForward != nil {
		set = append(set, originRuleTypeNonFastForward)
	}
	if m.BlockDirectUpdates != nil {
		set = append(set, originRuleTypeBlockDirectUpdates)
	}
	if m.BlockMerges != nil {
		set = append(set, originRuleTypeBlockMerges)
	}
	if m.RequiredLinearHistory != nil {
		set = append(set, originRuleTypeRequiredLinearHistory)
	}
	if m.RefNamePattern != nil {
		set = append(set, originRuleTypeRefNamePattern)
	}
	if m.MaxFileSize != nil {
		set = append(set, originRuleTypeMaxFileSize)
	}
	return set
}

// ruleType returns the Origin rule type of the single typed block on this
// rule, or an error when zero or several blocks are set.
func (m originRepoRulesetRuleModel) ruleType() (string, error) {
	set := m.typedBlocks()
	if len(set) != 1 {
		return "", fmt.Errorf("must set exactly one of %s", strings.Join(originRuleTypeNames(), ", "))
	}
	return set[0], nil
}

// validate checks the typed block on this rule against the ruleset kind.
func (m originRepoRulesetRuleModel) validate(name string, kind types.String) error {
	ruleType, err := m.ruleType()
	if err != nil {
		return fmt.Errorf("%s %w", name, err)
	}
	if !kind.IsNull() && !kind.IsUnknown() && !originRuleAllowedOnKind(ruleType, kind.ValueString()) {
		return originRuleKindError(name, ruleType)
	}
	switch ruleType {
	case originRuleTypePullRequest:
		count := m.PullRequest.RequiredApprovingReviewCount
		if !count.IsNull() && !count.IsUnknown() && (count.ValueInt64() < 0 || count.ValueInt64() > maxOriginRequiredApprovingReviews) {
			return fmt.Errorf("%s.pull_request.required_approving_review_count must be between 0 and %d", name, maxOriginRequiredApprovingReviews)
		}
		if err := validateAllowedMergeMethods(name, m.PullRequest.AllowedMergeMethods); err != nil {
			return err
		}
		if err := validateRequiredReviewers(name, m.PullRequest.RequiredReviewers); err != nil {
			return err
		}
	case originRuleTypeRequireStatusChecks:
		for i, check := range m.RequireStatusChecks.RequiredChecks {
			prefix := fmt.Sprintf("%s.require_status_checks.required_check[%d]", name, i)
			if err := requireNonEmptyUnpadded(check.ActorKind, prefix+".actor_kind"); err != nil {
				return err
			}
			if err := requireNonEmptyUnpadded(check.ActorID, prefix+".actor_id"); err != nil {
				return err
			}
			if err := requireNonEmptyUnpadded(check.GroupKey, prefix+".group_key"); err != nil {
				return err
			}
			if !check.RunKey.IsNull() {
				if err := requireNonEmptyUnpadded(check.RunKey, prefix+".run_key"); err != nil {
					return err
				}
			}
			if !check.Name.IsNull() {
				if err := requireNonEmptyUnpadded(check.Name, prefix+".name"); err != nil {
					return err
				}
			}
		}
	case originRuleTypeRefNamePattern:
		if err := requireNonEmptyUnpadded(m.RefNamePattern.Pattern, name+".ref_name_pattern.pattern"); err != nil {
			return err
		}
	case originRuleTypeMaxFileSize:
		size := m.MaxFileSize.MaxFileSizeBytes
		if size.IsNull() {
			return fmt.Errorf("%s.max_file_size.max_file_size_bytes is required", name)
		}
		if !size.IsUnknown() && (size.ValueInt64() < 1 || size.ValueInt64() > maxOriginFileSizeBytes) {
			return fmt.Errorf("%s.max_file_size.max_file_size_bytes must be between 1 and %d", name, maxOriginFileSizeBytes)
		}
	}
	return nil
}

func validateAllowedMergeMethods(name string, methods types.List) error {
	if methods.IsNull() || methods.IsUnknown() {
		return nil
	}
	values, err := stringValues(methods)
	if err != nil {
		return fmt.Errorf("%s.pull_request.allowed_merge_methods: %w", name, err)
	}
	if len(values) == 0 {
		return fmt.Errorf("%s.pull_request.allowed_merge_methods must list merge, squash, or both; an empty list is rejected", name)
	}
	seen := make(map[string]struct{}, len(values))
	for i, method := range values {
		if strings.TrimSpace(method) != method {
			return fmt.Errorf("%s.pull_request.allowed_merge_methods[%d] must not have leading or trailing spaces", name, i)
		}
		if method != originMergeMethodMerge && method != originMergeMethodSquash {
			return fmt.Errorf("%s.pull_request.allowed_merge_methods[%d] must be %s or %s", name, i, originMergeMethodMerge, originMergeMethodSquash)
		}
		if _, ok := seen[method]; ok {
			return fmt.Errorf("%s.pull_request.allowed_merge_methods lists %s more than once", name, method)
		}
		seen[method] = struct{}{}
	}
	return nil
}

func validateRequiredReviewers(name string, reviewers []originRequiredReviewerModel) error {
	if len(reviewers) > maxOriginRequiredReviewers {
		return fmt.Errorf("%s.pull_request.required_reviewer has %d entries; the Origin API allows at most %d", name, len(reviewers), maxOriginRequiredReviewers)
	}
	seen := make(map[string]struct{}, len(reviewers))
	for i, reviewer := range reviewers {
		prefix := fmt.Sprintf("%s.pull_request.required_reviewer[%d]", name, i)
		if reviewer.Group == nil {
			return fmt.Errorf("%s.group is required", prefix)
		}
		if err := requireGroupPublicID(reviewer.Group.ID, prefix+".group.id"); err != nil {
			return err
		}
		if !reviewer.Group.ID.IsNull() && !reviewer.Group.ID.IsUnknown() {
			id := reviewer.Group.ID.ValueString()
			if _, ok := seen[id]; ok {
				return fmt.Errorf("%s.group.id %s is listed more than once", prefix, id)
			}
			seen[id] = struct{}{}
		}
		approvals := reviewer.MinimumApprovals
		if !approvals.IsNull() && !approvals.IsUnknown() && (approvals.ValueInt64() < 1 || approvals.ValueInt64() > maxOriginReviewerApprovals) {
			return fmt.Errorf("%s.minimum_approvals must be between 1 and %d", prefix, maxOriginReviewerApprovals)
		}
	}
	return nil
}

// parameters encodes the typed block as the Origin parameters object. Rules
// without parameters return nil so the field is omitted on the wire.
func (m originRepoRulesetRuleModel) parameters() (json.RawMessage, error) {
	ruleType, err := m.ruleType()
	if err != nil {
		return nil, err
	}
	var wire any
	switch ruleType {
	case originRuleTypePullRequest:
		methods, err := stringValues(m.PullRequest.AllowedMergeMethods)
		if err != nil {
			return nil, fmt.Errorf("pull_request.allowed_merge_methods is incomplete")
		}
		reviewers := make([]originRequiredReviewerParameters, 0, len(m.PullRequest.RequiredReviewers))
		for i, reviewer := range m.PullRequest.RequiredReviewers {
			if reviewer.Group == nil || reviewer.Group.ID.IsUnknown() || reviewer.MinimumApprovals.IsUnknown() {
				return nil, fmt.Errorf("pull_request.required_reviewer[%d] is incomplete", i)
			}
			reviewers = append(reviewers, originRequiredReviewerParameters{
				Group:            originRequiredReviewerGroupParameters{ID: reviewer.Group.ID.ValueString()},
				MinimumApprovals: int64Pointer(reviewer.MinimumApprovals),
			})
		}
		params := originPullRequestParameters{
			RequiredApprovingReviewCount:   int64Pointer(m.PullRequest.RequiredApprovingReviewCount),
			DismissStaleReviewsOnPush:      boolPointer(m.PullRequest.DismissStaleReviewsOnPush),
			RequireCodeOwnerReview:         boolPointer(m.PullRequest.RequireCodeOwnerReview),
			RequireLastPushApproval:        boolPointer(m.PullRequest.RequireLastPushApproval),
			RequiredReviewThreadResolution: boolPointer(m.PullRequest.RequiredReviewThreadResolution),
			AllowedMergeMethods:            methods,
			RequiredReviewers:              reviewers,
		}
		if params.empty() {
			return nil, nil
		}
		wire = params
	case originRuleTypeRequireStatusChecks:
		params := originRequireStatusChecksParameters{
			RequiredChecks: make([]originRequiredCheckParameters, 0, len(m.RequireStatusChecks.RequiredChecks)),
		}
		for i, check := range m.RequireStatusChecks.RequiredChecks {
			if check.ActorKind.IsUnknown() || check.ActorID.IsUnknown() || check.GroupKey.IsUnknown() || check.RunKey.IsUnknown() || check.Name.IsUnknown() {
				return nil, fmt.Errorf("require_status_checks.required_check[%d] is incomplete", i)
			}
			params.RequiredChecks = append(params.RequiredChecks, originRequiredCheckParameters{
				ActorKind: check.ActorKind.ValueString(),
				ActorID:   check.ActorID.ValueString(),
				GroupKey:  check.GroupKey.ValueString(),
				RunKey:    check.RunKey.ValueString(),
				Name:      check.Name.ValueString(),
			})
		}
		wire = params
	case originRuleTypeRefNamePattern:
		if m.RefNamePattern.Pattern.IsUnknown() || m.RefNamePattern.Negate.IsUnknown() {
			return nil, fmt.Errorf("ref_name_pattern is incomplete")
		}
		wire = originRefNamePatternParameters{
			Pattern: m.RefNamePattern.Pattern.ValueString(),
			Negate:  boolPointer(m.RefNamePattern.Negate),
		}
	case originRuleTypeMaxFileSize:
		if m.MaxFileSize == nil || m.MaxFileSize.MaxFileSizeBytes.IsUnknown() {
			return nil, fmt.Errorf("max_file_size is incomplete")
		}
		if m.MaxFileSize.MaxFileSizeBytes.IsNull() {
			return nil, fmt.Errorf("max_file_size.max_file_size_bytes is required")
		}
		wire = originMaxFileSizeParameters{MaxFileSizeBytes: int64Pointer(m.MaxFileSize.MaxFileSizeBytes)}
	default:
		return nil, nil
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("encoding %s parameters: %w", ruleType, err)
	}
	return json.RawMessage(encoded), nil
}

func int64Pointer(value types.Int64) *int64 {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	v := value.ValueInt64()
	return &v
}

func boolPointer(value types.Bool) *bool {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	v := value.ValueBool()
	return &v
}

func int64FromPointer(value *int64) types.Int64 {
	if value == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*value)
}

func boolFromPointer(value *bool) types.Bool {
	if value == nil {
		return types.BoolNull()
	}
	return types.BoolValue(*value)
}

// ruleModelFromAPI decodes an Origin rule into its typed block.
func ruleModelFromAPI(rule originRulesetRule) (originRepoRulesetRuleModel, error) {
	if strings.TrimSpace(rule.ID) == "" {
		return originRepoRulesetRuleModel{}, fmt.Errorf("Origin API returned a rule without an id")
	}
	model := originRepoRulesetRuleModel{ID: types.StringValue(rule.ID)}
	raw := rule.Parameters
	if len(strings.TrimSpace(string(raw))) == 0 || string(raw) == "null" {
		raw = json.RawMessage("{}")
	}
	switch rule.RuleType {
	case originRuleTypePullRequest:
		if err := rejectLegacyPullRequestReviewerTeam(raw); err != nil {
			return originRepoRulesetRuleModel{}, fmt.Errorf("rule %q parameters: %w", rule.ID, err)
		}
		var params originPullRequestParameters
		if err := json.Unmarshal(raw, &params); err != nil {
			return originRepoRulesetRuleModel{}, fmt.Errorf("rule %q parameters: %w", rule.ID, err)
		}
		model.PullRequest = &originPullRequestRuleModel{
			RequiredApprovingReviewCount:   int64FromPointer(params.RequiredApprovingReviewCount),
			DismissStaleReviewsOnPush:      boolFromPointer(params.DismissStaleReviewsOnPush),
			RequireCodeOwnerReview:         boolFromPointer(params.RequireCodeOwnerReview),
			RequireLastPushApproval:        boolFromPointer(params.RequireLastPushApproval),
			RequiredReviewThreadResolution: boolFromPointer(params.RequiredReviewThreadResolution),
			AllowedMergeMethods:            stringListOrNull(params.AllowedMergeMethods),
			RequiredReviewers:              requiredReviewersFromAPI(params.RequiredReviewers),
		}
	case originRuleTypeRequireStatusChecks:
		var params originRequireStatusChecksParameters
		if err := json.Unmarshal(raw, &params); err != nil {
			return originRepoRulesetRuleModel{}, fmt.Errorf("rule %q parameters: %w", rule.ID, err)
		}
		block := &originRequireStatusChecksRuleModel{
			RequiredChecks: make([]originRequiredCheckModel, 0, len(params.RequiredChecks)),
		}
		for _, check := range params.RequiredChecks {
			block.RequiredChecks = append(block.RequiredChecks, originRequiredCheckModel{
				ActorKind: types.StringValue(check.ActorKind),
				ActorID:   types.StringValue(check.ActorID),
				GroupKey:  types.StringValue(check.GroupKey),
				RunKey:    stringOrNull(check.RunKey),
				Name:      stringOrNull(check.Name),
			})
		}
		model.RequireStatusChecks = block
	case originRuleTypeRefNamePattern:
		var params originRefNamePatternParameters
		if err := json.Unmarshal(raw, &params); err != nil {
			return originRepoRulesetRuleModel{}, fmt.Errorf("rule %q parameters: %w", rule.ID, err)
		}
		model.RefNamePattern = &originRefNamePatternRuleModel{
			Pattern: types.StringValue(params.Pattern),
			Negate:  boolFromPointer(params.Negate),
		}
	case originRuleTypeRequireBranchUpToDate:
		model.RequireBranchUpToDate = &originEmptyRuleModel{}
	case originRuleTypeDeletion:
		model.Deletion = &originEmptyRuleModel{}
	case originRuleTypeNonFastForward:
		model.NonFastForward = &originEmptyRuleModel{}
	case originRuleTypeBlockDirectUpdates:
		model.BlockDirectUpdates = &originEmptyRuleModel{}
	case originRuleTypeBlockMerges:
		model.BlockMerges = &originEmptyRuleModel{}
	case originRuleTypeRequiredLinearHistory:
		model.RequiredLinearHistory = &originEmptyRuleModel{}
	case originRuleTypeMaxFileSize:
		var params originMaxFileSizeParameters
		if err := json.Unmarshal(raw, &params); err != nil {
			return originRepoRulesetRuleModel{}, fmt.Errorf("rule %q parameters: %w", rule.ID, err)
		}
		model.MaxFileSize = &originMaxFileSizeRuleModel{MaxFileSizeBytes: int64FromPointer(params.MaxFileSizeBytes)}
	default:
		return originRepoRulesetRuleModel{}, fmt.Errorf("Origin API returned rule %q with type %q, which this provider does not support; supported types are %s", rule.ID, rule.RuleType, strings.Join(originRuleTypeNames(), ", "))
	}
	return model, nil
}

// ruleStateFromAPI maps an Origin rule onto the rule the configuration asked
// for. The rule ID always comes from Origin. On apply the typed block keeps the
// planned values, so state matches the plan. On refresh, attributes the
// configuration manages take the Origin value and attributes left unset stay
// unset, so server-side defaults do not show up as drift.
func ruleStateFromAPI(want originRepoRulesetRuleModel, rule originRulesetRule, apply bool) (originRepoRulesetRuleModel, error) {
	fromAPI, err := ruleModelFromAPI(rule)
	if err != nil {
		return originRepoRulesetRuleModel{}, err
	}
	if apply {
		want.ID = fromAPI.ID
		return want, nil
	}
	merged := fromAPI
	if want.PullRequest != nil && fromAPI.PullRequest != nil {
		block := *fromAPI.PullRequest
		block.RequiredApprovingReviewCount = mergeInt64(want.PullRequest.RequiredApprovingReviewCount, block.RequiredApprovingReviewCount)
		block.DismissStaleReviewsOnPush = mergeBool(want.PullRequest.DismissStaleReviewsOnPush, block.DismissStaleReviewsOnPush)
		block.RequireCodeOwnerReview = mergeBool(want.PullRequest.RequireCodeOwnerReview, block.RequireCodeOwnerReview)
		block.RequireLastPushApproval = mergeBool(want.PullRequest.RequireLastPushApproval, block.RequireLastPushApproval)
		block.RequiredReviewThreadResolution = mergeBool(want.PullRequest.RequiredReviewThreadResolution, block.RequiredReviewThreadResolution)
		block.AllowedMergeMethods = mergeStringList(want.PullRequest.AllowedMergeMethods, block.AllowedMergeMethods)
		block.RequiredReviewers = mergeRequiredReviewers(want.PullRequest.RequiredReviewers, block.RequiredReviewers)
		merged.PullRequest = &block
	}
	if want.MaxFileSize != nil && fromAPI.MaxFileSize != nil {
		merged.MaxFileSize = fromAPI.MaxFileSize
	}
	if want.RefNamePattern != nil && fromAPI.RefNamePattern != nil {
		block := *fromAPI.RefNamePattern
		block.Negate = mergeBool(want.RefNamePattern.Negate, block.Negate)
		merged.RefNamePattern = &block
	}
	if want.RequireStatusChecks != nil && fromAPI.RequireStatusChecks != nil {
		merged.RequireStatusChecks = &originRequireStatusChecksRuleModel{
			RequiredChecks: mergeRequiredChecks(want.RequireStatusChecks.RequiredChecks, fromAPI.RequireStatusChecks.RequiredChecks),
		}
	}
	return merged, nil
}

// mergeRequiredChecks refreshes required_check blocks. Each prior check is
// paired with the Origin check that has the same actor and group key (and the
// same run key when the prior sets one); the pair keeps prior nulls for
// run_key and name. Origin checks with no prior counterpart are appended as
// reported, and prior checks Origin no longer reports are dropped.
func mergeRequiredChecks(prior, api []originRequiredCheckModel) []originRequiredCheckModel {
	used := make([]bool, len(api))
	out := make([]originRequiredCheckModel, 0, len(api))
	for _, want := range prior {
		match := -1
		for i, got := range api {
			if used[i] || !requiredCheckIdentityEqual(want, got) {
				continue
			}
			match = i
			break
		}
		if match < 0 {
			continue
		}
		used[match] = true
		got := api[match]
		got.RunKey = mergeString(want.RunKey, got.RunKey)
		got.Name = mergeString(want.Name, got.Name)
		out = append(out, got)
	}
	for i, got := range api {
		if !used[i] {
			out = append(out, got)
		}
	}
	return out
}

func requiredCheckIdentityEqual(want, got originRequiredCheckModel) bool {
	if !want.ActorKind.Equal(got.ActorKind) || !want.ActorID.Equal(got.ActorID) || !want.GroupKey.Equal(got.GroupKey) {
		return false
	}
	if !want.RunKey.IsNull() && !want.RunKey.IsUnknown() && !want.RunKey.Equal(got.RunKey) {
		return false
	}
	return true
}

// mergeRequiredReviewers refreshes required_reviewer blocks. Each prior reviewer
// is paired with the Origin reviewer that names the same group; minimum_approvals
// stays null when the configuration left it unset. Origin reviewers with no prior
// counterpart are appended, and prior reviewers Origin no longer reports are dropped.
// A configuration that does not set required_reviewer keeps it unset.
func mergeRequiredReviewers(prior, api []originRequiredReviewerModel) []originRequiredReviewerModel {
	if prior == nil {
		return nil
	}
	if api == nil {
		return prior
	}
	used := make([]bool, len(api))
	out := make([]originRequiredReviewerModel, 0, len(api))
	for _, want := range prior {
		match := -1
		for i, got := range api {
			if used[i] || !requiredReviewerIdentityEqual(want, got) {
				continue
			}
			match = i
			break
		}
		if match < 0 {
			continue
		}
		used[match] = true
		got := api[match]
		got.MinimumApprovals = mergeInt64(want.MinimumApprovals, got.MinimumApprovals)
		out = append(out, got)
	}
	for i, got := range api {
		if !used[i] {
			out = append(out, got)
		}
	}
	return out
}

func requiredReviewerIdentityEqual(want, got originRequiredReviewerModel) bool {
	if want.Group == nil || got.Group == nil {
		return false
	}
	return want.Group.ID.Equal(got.Group.ID)
}

func mergeStringList(prior, api types.List) types.List {
	if prior.IsNull() || prior.IsUnknown() {
		return types.ListNull(types.StringType)
	}
	if api.IsNull() || api.IsUnknown() {
		return prior
	}
	return api
}

func requiredReviewersFromAPI(reviewers []originRequiredReviewerParameters) []originRequiredReviewerModel {
	if reviewers == nil {
		return nil
	}
	out := make([]originRequiredReviewerModel, 0, len(reviewers))
	for _, reviewer := range reviewers {
		out = append(out, originRequiredReviewerModel{
			Group:            &originRequiredReviewerGroupModel{ID: types.StringValue(reviewer.Group.ID)},
			MinimumApprovals: int64FromPointer(reviewer.MinimumApprovals),
		})
	}
	return out
}

// rejectLegacyPullRequestReviewerTeam fails a read that still carries the
// removed requiredReviewers[].team shape. Decoding that shape into the group
// field would drop the reviewer on the next update and the rule would fail open.
func rejectLegacyPullRequestReviewerTeam(raw json.RawMessage) error {
	var probe struct {
		RequiredReviewers []struct {
			Group json.RawMessage `json:"group"`
			Team  json.RawMessage `json:"team"`
		} `json:"requiredReviewers"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return err
	}
	for i, reviewer := range probe.RequiredReviewers {
		if jsonPresent(reviewer.Team) && !jsonPresent(reviewer.Group) {
			return fmt.Errorf("requiredReviewers[%d] uses the removed team shape; the Origin API names the group with group.id (%s...)", i, originGroupIDPrefix)
		}
	}
	return nil
}

func jsonPresent(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null"
}

// ensureStringList gives an unset list the element type Terraform requires
// when writing state. A composite literal leaves types.List as a null value
// with no element type, and state.Set rejects that.
func ensureStringList(list types.List) types.List {
	if list.ElementType(context.Background()) != nil {
		return list
	}
	if list.IsUnknown() {
		return types.ListUnknown(types.StringType)
	}
	return types.ListNull(types.StringType)
}

func stringListOrNull(values []string) types.List {
	if values == nil {
		return types.ListNull(types.StringType)
	}
	elems := make([]attr.Value, len(values))
	for i, value := range values {
		elems[i] = types.StringValue(value)
	}
	return types.ListValueMust(types.StringType, elems)
}

// stringValues returns the list as strings. A null list returns nil, nil.
// An unknown list or element returns an error.
func stringValues(list types.List) ([]string, error) {
	if list.IsNull() {
		return nil, nil
	}
	if list.IsUnknown() {
		return nil, fmt.Errorf("value is unknown")
	}
	elements := list.Elements()
	values := make([]string, len(elements))
	for i, element := range elements {
		value, ok := element.(types.String)
		if !ok || value.IsNull() || value.IsUnknown() {
			return nil, fmt.Errorf("element %d is unknown", i)
		}
		values[i] = value.ValueString()
	}
	return values, nil
}

func mergeString(prior, api types.String) types.String {
	if prior.IsNull() {
		return types.StringNull()
	}
	if api.IsNull() {
		return prior
	}
	return api
}

// mergeInt64 applies the refresh rule for one optional attribute: unset in
// the prior state stays unset; otherwise the Origin value wins when present.
func mergeInt64(prior, api types.Int64) types.Int64 {
	if prior.IsNull() {
		return types.Int64Null()
	}
	if api.IsNull() {
		return prior
	}
	return api
}

func mergeBool(prior, api types.Bool) types.Bool {
	if prior.IsNull() {
		return types.BoolNull()
	}
	if api.IsNull() {
		return prior
	}
	return api
}

// ruleMatchesAPI reports whether an Origin rule has the same type as the
// configured rule and carries every parameter the configuration sets. A rule
// configured without parameters only matches an Origin rule whose parameters
// are empty; a configuration that cannot be encoded matches nothing.
func ruleMatchesAPI(want originRepoRulesetRuleModel, rule originRulesetRule) bool {
	wantType, err := want.ruleType()
	if err != nil || wantType != rule.RuleType {
		return false
	}
	wantParams, err := want.parameters()
	if err != nil {
		return false
	}
	gotMap, err := decodeParameterObject(rule.Parameters)
	if err != nil {
		return false
	}
	if wantParams == nil {
		return len(gotMap) == 0
	}
	var wantMap map[string]any
	if err := json.Unmarshal(wantParams, &wantMap); err != nil {
		return false
	}
	for key, value := range wantMap {
		if !reflect.DeepEqual(gotMap[key], value) {
			return false
		}
	}
	return true
}

// decodeParameterObject decodes an Origin parameters object, treating an
// absent or null value as empty.
func decodeParameterObject(raw json.RawMessage) (map[string]any, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return map[string]any{}, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	if decoded == nil {
		decoded = map[string]any{}
	}
	return decoded, nil
}
