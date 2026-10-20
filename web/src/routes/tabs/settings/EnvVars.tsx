import { Input, Button, Chip, Table, TextField, FieldError } from "@heroui/react";
import { Lock, Plus, Type, Trash2 } from "lucide-react";
import type { EnvVar } from "@/types";
import type { SectionProps } from "./types";
import { useOptionalIntl } from "@/lib/i18n/format";

const ENV_NAME = /^[A-Z_][A-Z0-9_]*$/;

export function EnvVarsSection({ draft, onChange }: SectionProps) {
  const intl = useOptionalIntl();
  const env = draft.spec.env ?? [];

  const setEnv = (next: EnvVar[]) => {
    onChange({
      ...draft,
      spec: { ...draft.spec, env: next.length ? next : undefined },
    });
  };

  const update = (idx: number, patch: EnvVar) => {
    setEnv(env.map((v, i) => (i === idx ? patch : v)));
  };
  const remove = (idx: number) => setEnv(env.filter((_, i) => i !== idx));
  const add = (kind: "literal" | "secret") => {
    if (kind === "literal") setEnv([...env, { name: "", value: "" }]);
    else setEnv([...env, { name: "", valueFrom: { secretKeyRef: { name: "", key: "" } } }]);
  };

  const seen = new Set<string>();
  const dupes = new Set<string>();
  for (const v of env) {
    if (v.name) {
      if (seen.has(v.name)) dupes.add(v.name);
      else seen.add(v.name);
    }
  }

  return (
    <div className="space-y-4">
      {env.length === 0 && (
        <p className="text-sm text-default-500">{intl.formatMessage({ id: "web.routes.tabs.settings.env_vars.no_environment_variables_click_below_to", defaultMessage: "No environment variables. Click below to add one.", description: "Env vars: empty-state text shown when the server has no environment variables; points to the add button below" })}</p>
      )}

      {env.length > 0 && (
        <Table.Root>
          <Table.ScrollContainer>
            <Table.Content
              aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.env_vars.environment_variables", defaultMessage: "Environment variables", description: "Env vars: accessible label of the environment variables table, read by screen readers; not shown as text" })}
              className="mb-4 max-h-[400px]"
              keyboardNavigationBehavior="arrow"
            >
              <Table.Header>
                <Table.Column key="type" width="80" isRowHeader>{intl.formatMessage({ id: "web.routes.tabs.settings.env_vars.type", defaultMessage: "Type", description: "Table column header for an environment variable's source type (literal or secret)" })}</Table.Column>
                <Table.Column key="name" width="160">{intl.formatMessage({ id: "web.routes.tabs.settings.env_vars.name", defaultMessage: "Name", description: "Table column header for an environment variable's name" })}</Table.Column>
                <Table.Column key="value" width="100%">{intl.formatMessage({ id: "web.routes.tabs.settings.env_vars.value", defaultMessage: "Value", description: "Table column header for an environment variable's value" })}</Table.Column>
                <Table.Column key="actions" id="actions" width="40" className="text-end">{intl.formatMessage({ id: "web.routes.tabs.settings.env_vars.actions", defaultMessage: "Actions", description: "Table column header for the per-row action buttons" })}</Table.Column>
              </Table.Header>
              <Table.Body>
                {env.map((v, idx) => {
                  const isSecret = !!v.valueFrom?.secretKeyRef;
                  const nameInvalid = v.name !== "" && !ENV_NAME.test(v.name);
                  const dup = v.name !== "" && dupes.has(v.name);
                  return (
                    <Table.Row key={idx}>
                      <Table.Cell>
                        <Chip
                          size="sm"
                          variant="soft"
                          className="text-xs"
                        >
                          {isSecret ? (
                            <>
                              <Lock className="h-3 w-3" />{intl.formatMessage({ id: "web.routes.tabs.settings.env_vars.secret", defaultMessage: "Secret", description: "Option label for an environment variable whose value comes from a Kubernetes Secret" })}</>
                          ) : (
                            <>
                              <Type className="h-3 w-3" />{intl.formatMessage({ id: "web.routes.tabs.settings.env_vars.literal", defaultMessage: "Literal", description: "Option label for an environment variable with a literal value typed in the dashboard" })}</>
                          )}
                        </Chip>
                      </Table.Cell>
                      <Table.Cell>
                        <TextField isInvalid={nameInvalid || dup} className="space-y-1">
                          <Input
                            aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.env_vars.variable_name", defaultMessage: "Variable name", description: "Env vars: accessible label of the variable name box in a row of the environment variables table; not shown as text" })}
                            value={v.name}
                            onChange={(e) => update(idx, { ...v, name: e.target.value })}
                            placeholder={intl.formatMessage({ id: "web.routes.tabs.settings.env_vars.var_name", defaultMessage: "VAR_NAME", description: "Env vars: example placeholder in the variable name box, showing the naming style; an example name, not a default" })}
                            spellCheck={false}
                            className="text-xs"
                          />
                          {nameInvalid && (
                            <FieldError className="text-xs">{intl.formatMessage({ id: "web.routes.tabs.settings.env_vars.must_match_a_z_a_z0_9", defaultMessage: "Must match [A-Z_][A-Z0-9_]*", description: "Env vars: validation error under the variable name box when the name does not match the allowed upper-case pattern" })}</FieldError>
                          )}
                          {dup && (
                            <FieldError className="text-xs">{intl.formatMessage({ id: "web.routes.tabs.settings.env_vars.duplicate_name", defaultMessage: "Duplicate name", description: "Env vars: validation error under the variable name box when another variable already uses that name" })}</FieldError>
                          )}
                        </TextField>
                      </Table.Cell>
                      <Table.Cell>
                        {isSecret ? (
                          <div className="space-y-2">
                            <div className="flex gap-2">
                              <Input
                                aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.env_vars.secret_name", defaultMessage: "Secret name", description: "Env vars: accessible label of the box that holds the Kubernetes Secret name for a secret-backed variable; not shown as text" })}
                                value={v.valueFrom?.secretKeyRef?.name ?? ""}
                                onChange={(e) =>
                                  update(idx, {
                                    ...v,
                                    valueFrom: {
                                      secretKeyRef: {
                                        name: e.target.value,
                                        key: v.valueFrom?.secretKeyRef?.key ?? "",
                                      },
                                    },
                                  })
                                }
                                placeholder={intl.formatMessage({ id: "web.routes.tabs.settings.env_vars.secret_name_2", defaultMessage: "secret-name", description: "Env vars: example placeholder in the Secret name box showing the expected format; an example value, not a default" })}
                                spellCheck={false}
                                className="text-xs flex-1"
                              />
                              <Input
                                aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.env_vars.secret_key", defaultMessage: "Secret key", description: "Env vars: accessible label of the box that holds the key inside the Kubernetes Secret for a secret-backed variable; not shown as text" })}
                                value={v.valueFrom?.secretKeyRef?.key ?? ""}
                                onChange={(e) =>
                                  update(idx, {
                                    ...v,
                                    valueFrom: {
                                      secretKeyRef: {
                                        name: v.valueFrom?.secretKeyRef?.name ?? "",
                                        key: e.target.value,
                                      },
                                    },
                                  })
                                }
                                placeholder={intl.formatMessage({ id: "web.routes.tabs.settings.env_vars.key", defaultMessage: "key", description: "Env vars: placeholder in an environment variable row's key box, shown before typing; the value box beside it has its own placeholder" })}
                                spellCheck={false}
                                className="text-xs flex-1"
                              />
                            </div>
                          </div>
                        ) : (
                          <Input
                            aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.env_vars.variable_value", defaultMessage: "Variable value", description: "Env vars: accessible label of the box that holds the value of a variable row; not shown as text" })}
                            value={v.value ?? ""}
                            onChange={(e) => update(idx, { ...v, value: e.target.value })}
                            placeholder={intl.formatMessage({ id: "web.routes.tabs.settings.env_vars.value_2", defaultMessage: "value", description: "Env vars: placeholder in a variable row's value box, shown before typing a literal value" })}
                            spellCheck={false}
                            className="text-xs"
                          />
                        )}
                      </Table.Cell>
                      <Table.Cell>
                        <Button
                          isIconOnly
                          size="sm"
                          variant="ghost"
                          aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.env_vars.remove", defaultMessage: "Remove", description: "Button that removes this environment variable row" })}
                          onPress={() => remove(idx)}
                          className="text-danger hover:bg-danger/10"
                        >
                          <Trash2 className="h-4 w-4" />
                        </Button>
                      </Table.Cell>
                    </Table.Row>
                  );
                })}
              </Table.Body>
            </Table.Content>
          </Table.ScrollContainer>
        </Table.Root>
      )}

      <div className="flex items-center gap-2">
        <Button
          size="sm"
          variant="outline"
          onPress={() => add("literal")}
        >
          <Plus className="h-4 w-4" />{intl.formatMessage({ id: "web.routes.tabs.settings.env_vars.add_variable", defaultMessage: "Add variable", description: "Button that adds a literal environment variable row" })}</Button>
        <Button
          size="sm"
          variant="outline"
          onPress={() => add("secret")}
        >
          <Lock className="h-4 w-4" />{intl.formatMessage({ id: "web.routes.tabs.settings.env_vars.add_from_secret", defaultMessage: "Add from secret", description: "Button that adds an environment variable whose value is read from a Secret" })}</Button>
      </div>
    </div>
  );
}
