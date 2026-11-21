import { useQuery } from "@tanstack/react-query";
import {
  ArrowDown,
  ArrowRight,
  ArrowUp,
  CircleAlert,
  ListX,
  Plus,
  Trash2,
  TriangleAlert,
} from "lucide-react";
import { Button, Input as HeroInput, ListBox, ListBoxItem, Select } from "@heroui/react";
import { defineMessages, type IntlShape } from "react-intl";
import { Roles } from "@/lib/endpoints";
import type {
  AuthCfg,
  AuthProvider,
  AuthRoleMappings,
  AuthRoleRule,
  HelmRoleOverride,
  OIDCHelmProvider,
} from "@/lib/config";
import { cn } from "@/lib/utils";
import { useOptionalIntl } from "@/lib/i18n/format";

// Spec 034: ordered SSO group → role rules (first match wins) with a
// fallback role, edited in the add-provider form and the Helm override card
// (design UdnBT / mrF6f). The pure helpers below mirror the server's reading
// of the old per-role roleMappings form (api/internal/auth/oidc.go).

/** The fallback value that refuses sign-in when no rule matches. */
export const DENY_FALLBACK = "deny";

const BUILTIN_ROLES = ["admin", "operator", "viewer"] as const;

const messages = defineMessages({
  title: { id: "web.components.ui.admin.role_rules_editor.role_rules", defaultMessage: "Role rules", description: "Role rules editor: heading of the ordered list of identity-provider group to role rules; keep it short" },
  hint: { id: "web.components.ui.admin.role_rules_editor.rules_are_checked_top_to_bottom", defaultMessage: "Rules are checked top to bottom. The first group the user belongs to decides their role.", description: "Role rules editor: explanation under the Role rules heading: rules are checked in order and the first group the user is in decides the role" },
  empty: { id: "web.components.ui.admin.role_rules_editor.no_rules", defaultMessage: "No rules. SSO users get viewer and their role is never changed at sign-in.", description: "Role rules editor: empty state when no rule is set; viewer is the role name and stays untranslated" },
  groupPlaceholder: { id: "web.components.ui.admin.role_rules_editor.idp_group_name", defaultMessage: "IdP group name", description: "Role rules editor: grey placeholder inside the group field of a rule, shown only while it is empty" },
  add: { id: "web.components.ui.admin.role_rules_editor.add_rule", defaultMessage: "Add rule", description: "Role rules editor: button that adds an empty rule at the end of the list; keep it short" },
  fallback: { id: "web.components.ui.admin.role_rules_editor.fallback_role", defaultMessage: "Fallback role", description: "Role rules editor: label of the dropdown choosing the role used when no rule matches; also a row label in the Helm provider details; keep it short" },
  fallbackHint: { id: "web.components.ui.admin.role_rules_editor.used_when_no_rule_matches", defaultMessage: "Used when no rule matches. Users whose role was set by hand are always let in.", description: "Role rules editor: hint under the fallback role dropdown" },
  refuse: { id: "web.components.ui.admin.role_rules_editor.refuse_sign_in", defaultMessage: "Refuse sign-in", description: "Role rules editor: fallback option that refuses the sign-in when no rule matches; keep it short" },
});

/** Old per-role mappings read as rules: admin, then operator, then viewer. */
export function rulesFromMappings(m?: AuthRoleMappings): AuthRoleRule[] {
  const out: AuthRoleRule[] = [];
  for (const role of BUILTIN_ROLES) {
    for (const group of m?.[role] ?? []) out.push({ group, role });
  }
  return out;
}

const hasKeys = (m?: AuthRoleMappings) => m !== undefined && Object.keys(m).length > 0;

/** The Helm-seeded rule list (GET returns roleRules; older APIs only roleMappings). */
export function helmRules(helm?: OIDCHelmProvider): AuthRoleRule[] {
  return helm?.roleRules ?? rulesFromMappings(helm?.roleMappings);
}

/** Whether the dashboard override replaces anything Helm set. */
export function isOverrideActive(ov?: HelmRoleOverride): boolean {
  return ov !== undefined && (ov.roleRules !== undefined || ov.defaultRole !== undefined || hasKeys(ov.roleMappings));
}

/** The rule list sign-in uses for the Helm provider with this override. */
export function effectiveOverrideRules(ov: HelmRoleOverride | undefined, helm?: OIDCHelmProvider): AuthRoleRule[] {
  if (ov?.roleRules !== undefined) return ov.roleRules;
  const base = helmRules(helm);
  const m = ov?.roleMappings;
  if (!m || !hasKeys(m)) return base;
  const hm = helm?.roleMappings;
  if (hm && hasKeys(hm)) {
    // Old-flag Helm seed: the override replaces each role it sets.
    return rulesFromMappings({
      admin: m.admin ?? hm.admin,
      operator: m.operator ?? hm.operator,
      viewer: m.viewer ?? hm.viewer,
    });
  }
  // Rule-list Helm seed: overrideRulesPerRole in api/internal/auth/oidc.go.
  const replaced = new Set<string>(BUILTIN_ROLES.filter((r) => m[r] !== undefined));
  return [...rulesFromMappings(m), ...base.filter((r) => !replaced.has(r.role))];
}

/** The fallback sign-in uses for the Helm provider with this override. */
export function effectiveOverrideFallback(ov: HelmRoleOverride | undefined, helm?: OIDCHelmProvider): string {
  return ov?.defaultRole ?? helm?.defaultRole ?? "";
}

// An old-form mapping that lists no group still has mapping on (the fallback,
// "deny" included, applies to everyone), but an empty roleRules list turns
// mapping off (oidc.go MappingOn). Such a mapping stays in the old form.
function providerToRuleForm(p: AuthProvider): AuthProvider {
  if (p.roleMappings === undefined || p.roleRules !== undefined) return p;
  const { roleMappings, ...rest } = p;
  const rules = rulesFromMappings(roleMappings);
  return rules.length > 0 ? { ...rest, roleRules: rules } : p;
}

/**
 * The auth section as the dashboard saves it (FR-005): providers and the Helm
 * override in the old roleMappings form are written as roleRules, and an
 * override that changes nothing is dropped.
 */
export function normalizeAuthForSave(cfg: AuthCfg, helm?: OIDCHelmProvider): AuthCfg {
  const next: AuthCfg = { ...cfg, providers: cfg.providers.map(providerToRuleForm) };
  const ov = cfg.helmOverride;
  if (ov === undefined) return next;
  if (!isOverrideActive(ov)) {
    delete next.helmOverride;
  } else if (ov.roleRules === undefined) {
    // An old-form override that leaves no rule (it cleared the last mapped
    // group) keeps mapping on with the fallback; as roleRules: [] it would
    // turn mapping off, so it stays in the old form (see providerToRuleForm).
    const rules = effectiveOverrideRules(ov, helm);
    if (rules.length > 0) {
      next.helmOverride = {
        roleRules: rules,
        ...(ov.defaultRole !== undefined ? { defaultRole: ov.defaultRole } : {}),
      };
    }
  }
  return next;
}

/** Each group's effective role: the role of the first rule naming it. */
function firstMatchRoles(rules: AuthRoleRule[]): Map<string, string> {
  const out = new Map<string, string>();
  for (const r of rules) {
    const group = r.group.trim();
    if (group && !out.has(group)) out.set(group, r.role);
  }
  return out;
}

/**
 * Groups a save would newly grant admin through the Helm override (FR-015):
 * groups whose first matching rule in the draft override grants admin, while
 * their first matching rule in the stored config (its override, or the Helm
 * rules when it has none) did not. A rule shadowed by an earlier rule for the
 * same group grants nothing, so moving it up is a new grant.
 */
export function addedOverrideAdminGroups(draft: AuthCfg, stored: AuthCfg, helm?: OIDCHelmProvider): string[] {
  if (!isOverrideActive(draft.helmOverride)) return [];
  const before = firstMatchRoles(
    isOverrideActive(stored.helmOverride) ? effectiveOverrideRules(stored.helmOverride, helm) : helmRules(helm),
  );
  const after = firstMatchRoles(effectiveOverrideRules(draft.helmOverride, helm));
  return [...after].filter(([group, role]) => role === "admin" && before.get(group) !== "admin").map(([group]) => group);
}

/** Role names for the rule and fallback dropdowns; the built-ins until /roles loads. */
export function useRoleNames(): { names: string[]; loaded: boolean } {
  const { data, isSuccess } = useQuery({ queryKey: ["roles"], queryFn: () => Roles.list() });
  return isSuccess ? { names: data.map((r) => r.name), loaded: true } : { names: [...BUILTIN_ROLES], loaded: false };
}

/** 1-based position of an earlier rule with the same group, or null. */
export function duplicateOf(rules: AuthRoleRule[], index: number): number | null {
  const group = rules[index].group.trim();
  if (!group) return null;
  const first = rules.findIndex((r) => r.group.trim() === group);
  return first < index ? first + 1 : null;
}

function roleMissing(role: string, names: string[], loaded: boolean): boolean {
  return loaded && !names.includes(role);
}

export function fallbackMissing(fallback: string, names: string[], loaded: boolean): boolean {
  return fallback !== "" && fallback !== DENY_FALLBACK && roleMissing(fallback, names, loaded);
}

/** A rule set the API would refuse: a blank group or a role that does not exist. */
export function rulesInvalid(rules: AuthRoleRule[], fallback: string, names: string[], loaded: boolean): boolean {
  return (
    rules.some((r) => r.group.trim() === "" || roleMissing(r.role, names, loaded)) ||
    fallbackMissing(fallback, names, loaded)
  );
}

/** Display text of a fallback value: "" reads as viewer, deny as "Refuse sign-in". */
export function fallbackLabel(intl: IntlShape, fallback: string): string {
  if (fallback === DENY_FALLBACK) return intl.formatMessage(messages.refuse);
  return fallback || "viewer";
}

export function fallbackRoleLabel(intl: IntlShape): string {
  return intl.formatMessage(messages.fallback);
}

export function RolePill({ role }: { role: string }) {
  return (
    <span
      className={cn(
        "rounded-full px-1.5 py-0.5 font-mono text-[11px] font-medium",
        role === "admin" ? "bg-warning-soft text-warning-soft-foreground" : "bg-default text-default-foreground",
      )}
    >
      {role}
    </span>
  );
}

function RuleNumber({ index }: { index: number }) {
  return (
    <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-surface-secondary font-mono text-[11px] text-muted">
      {index + 1}
    </span>
  );
}

export function EmptyRules() {
  const intl = useOptionalIntl();
  return (
    <div className="flex items-center gap-2 rounded-md border border-border px-3 py-2 text-xs italic text-muted">
      <ListX className="h-3.5 w-3.5 shrink-0" />
      {intl.formatMessage(messages.empty)}
    </div>
  );
}

/** Read-only, ordered rule list (Helm card, override card before an override). */
export function RoleRuleList({ rules, label }: { rules: AuthRoleRule[]; label: string }) {
  if (rules.length === 0) return <EmptyRules />;
  return (
    <ol className="space-y-1.5" aria-label={label}>
      {rules.map((r, i) => (
        <li key={i} className="flex items-center gap-3">
          <RuleNumber index={i} />
          <span className="min-w-0 flex-1 truncate font-mono text-xs">{r.group}</span>
          <ArrowRight className="h-3.5 w-3.5 shrink-0 text-muted" />
          <span className="w-32 shrink-0"><RolePill role={r.role} /></span>
        </li>
      ))}
    </ol>
  );
}

const triggerClass = "w-full rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80";

export interface RoleRulesEditorProps {
  rules: AuthRoleRule[];
  onRulesChange: (rules: AuthRoleRule[]) => void;
  /** "" (viewer), "deny" or a role name. */
  fallback: string;
  onFallbackChange: (fallback: string) => void;
  roleNames: string[];
  /** True once /roles answered: only then is a role reported missing. */
  rolesLoaded: boolean;
  /** The provider form locks the fallback until a rule exists (the API refuses defaultRole alone). */
  fallbackDisabled?: boolean;
}

export function RoleRulesEditor({
  rules,
  onRulesChange,
  fallback,
  onFallbackChange,
  roleNames,
  rolesLoaded,
  fallbackDisabled = false,
}: RoleRulesEditorProps) {
  const intl = useOptionalIntl();
  const title = intl.formatMessage(messages.title);
  const setRule = (index: number, patch: Partial<AuthRoleRule>) =>
    onRulesChange(rules.map((r, i) => (i === index ? { ...r, ...patch } : r)));
  const move = (index: number, by: -1 | 1) => {
    const next = [...rules];
    const [rule] = next.splice(index, 1);
    next.splice(index + by, 0, rule);
    onRulesChange(next);
  };
  const remove = (index: number) => onRulesChange(rules.filter((_, i) => i !== index));
  const add = () =>
    onRulesChange([...rules, { group: "", role: roleNames.includes("viewer") ? "viewer" : (roleNames[0] ?? "viewer") }]);
  const fallbackKey = fallback === "" ? "viewer" : fallback;
  const fallbackOptions =
    fallbackKey === DENY_FALLBACK || roleNames.includes(fallbackKey) ? roleNames : [...roleNames, fallbackKey];

  return (
    <div className="space-y-3">
      <div>
        <div className="text-sm font-medium">{title}</div>
        <p className="pt-0.5 text-xs text-muted">{intl.formatMessage(messages.hint)}</p>
      </div>
      {rules.length === 0 ? (
        <EmptyRules />
      ) : (
        <ol className="space-y-2" aria-label={title}>
          {rules.map((rule, i) => {
            const n = i + 1;
            const dup = duplicateOf(rules, i);
            const missing = roleMissing(rule.role, roleNames, rolesLoaded);
            const options = roleNames.includes(rule.role) ? roleNames : [...roleNames, rule.role];
            return (
              <li key={i} className="space-y-1">
                <div className="flex items-center gap-2">
                  <RuleNumber index={i} />
                  <HeroInput
                    aria-label={intl.formatMessage({ id: "web.components.ui.admin.role_rules_editor.rule_group", defaultMessage: "Rule {index} group", description: "Role rules editor: accessible name of the group name field of one rule; {index} is the rule's position, starting at 1" }, { index: n })}
                    placeholder={intl.formatMessage(messages.groupPlaceholder)}
                    value={rule.group}
                    onChange={(e) => setRule(i, { group: e.target.value })}
                    className={cn("min-w-0 flex-1", dup !== null && "border-warning")}
                  />
                  <Select
                    aria-label={intl.formatMessage({ id: "web.components.ui.admin.role_rules_editor.rule_role", defaultMessage: "Rule {index} role", description: "Role rules editor: accessible name of the role dropdown of one rule; {index} is the rule's position, starting at 1" }, { index: n })}
                    selectedKey={rule.role}
                    onSelectionChange={(key) => {
                      if (key !== null) setRule(i, { role: String(key) });
                    }}
                    className="w-40 shrink-0"
                  >
                    <Select.Trigger className={cn(triggerClass, missing && "border-danger")}>
                      <Select.Value />
                      <Select.Indicator className="ml-auto h-4 w-4" />
                    </Select.Trigger>
                    <Select.Popover className="rounded border border-border">
                      <ListBox aria-label={intl.formatMessage({ id: "web.components.ui.admin.role_rules_editor.rule_role", defaultMessage: "Rule {index} role", description: "Role rules editor: accessible name of the role dropdown of one rule; {index} is the rule's position, starting at 1" }, { index: n })}>
                        {options.map((name) => (
                          <ListBoxItem key={name} id={name}>{name}</ListBoxItem>
                        ))}
                      </ListBox>
                    </Select.Popover>
                  </Select>
                  <Button variant="ghost" size="sm" isIconOnly aria-label={intl.formatMessage({ id: "web.components.ui.admin.role_rules_editor.move_rule_up", defaultMessage: "Move rule {index} up", description: "Role rules editor: accessible name of the button that moves a rule one place up; {index} is the rule's position" }, { index: n })} isDisabled={i === 0} onPress={() => move(i, -1)}>
                    <ArrowUp className="h-4 w-4" />
                  </Button>
                  <Button variant="ghost" size="sm" isIconOnly aria-label={intl.formatMessage({ id: "web.components.ui.admin.role_rules_editor.move_rule_down", defaultMessage: "Move rule {index} down", description: "Role rules editor: accessible name of the button that moves a rule one place down; {index} is the rule's position" }, { index: n })} isDisabled={i === rules.length - 1} onPress={() => move(i, 1)}>
                    <ArrowDown className="h-4 w-4" />
                  </Button>
                  <Button variant="ghost" size="sm" isIconOnly aria-label={intl.formatMessage({ id: "web.components.ui.admin.role_rules_editor.remove_rule", defaultMessage: "Remove rule {index}", description: "Role rules editor: accessible name of the button that removes a rule; {index} is the rule's position" }, { index: n })} onPress={() => remove(i)}>
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </div>
                {dup !== null && (
                  <p className="flex items-center gap-1.5 pl-8 text-[11px] text-warning">
                    <TriangleAlert className="h-3 w-3 shrink-0" />
                    {intl.formatMessage({ id: "web.components.ui.admin.role_rules_editor.duplicate_group", defaultMessage: "This group is already matched by rule {index}, so this rule never applies.", description: "Role rules editor: warning under a rule whose group an earlier rule already uses; {index} is the earlier rule's position" }, { index: dup })}
                  </p>
                )}
                {missing && (
                  <p className="flex items-center gap-1.5 pl-8 text-[11px] text-danger">
                    <CircleAlert className="h-3 w-3 shrink-0" />
                    {intl.formatMessage({ id: "web.components.ui.admin.role_rules_editor.role_missing_rule", defaultMessage: "Role {role} no longer exists. Pick another role or remove this rule.", description: "Role rules editor: error under a rule whose role was deleted; {role} is the role name" }, { role: rule.role })}
                  </p>
                )}
              </li>
            );
          })}
        </ol>
      )}
      <Button variant="outline" size="sm" className="h-8 gap-1.5 text-[13px] font-medium" onPress={add}>
        <Plus className="h-3.5 w-3.5" />
        {intl.formatMessage(messages.add)}
      </Button>
      <div className="space-y-1">
        <div className="text-xs font-medium">{intl.formatMessage(messages.fallback)}</div>
        <Select
          aria-label={intl.formatMessage(messages.fallback)}
          selectedKey={fallbackKey}
          onSelectionChange={(key) => {
            if (key !== null) onFallbackChange(String(key));
          }}
          isDisabled={fallbackDisabled}
        >
          <Select.Trigger className={cn(triggerClass, fallbackMissing(fallback, roleNames, rolesLoaded) && "border-danger")}>
            <Select.Value />
            <Select.Indicator className="ml-auto h-4 w-4" />
          </Select.Trigger>
          <Select.Popover className="rounded border border-border">
            <ListBox aria-label={intl.formatMessage(messages.fallback)}>
              {[
                ...fallbackOptions.map((name) => (
                  <ListBoxItem key={name} id={name}>{name}</ListBoxItem>
                )),
                <ListBoxItem key={DENY_FALLBACK} id={DENY_FALLBACK}>{intl.formatMessage(messages.refuse)}</ListBoxItem>,
              ]}
            </ListBox>
          </Select.Popover>
        </Select>
        {fallbackMissing(fallback, roleNames, rolesLoaded) && (
          <p className="flex items-center gap-1.5 text-[11px] text-danger">
            <CircleAlert className="h-3 w-3 shrink-0" />
            {intl.formatMessage({ id: "web.components.ui.admin.role_rules_editor.role_missing_fallback", defaultMessage: "Role {role} no longer exists. Pick another role.", description: "Role rules editor: error under the fallback dropdown when its role was deleted; {role} is the role name" }, { role: fallback })}
          </p>
        )}
        <p className="text-[11px] text-muted">{intl.formatMessage(messages.fallbackHint)}</p>
      </div>
    </div>
  );
}
