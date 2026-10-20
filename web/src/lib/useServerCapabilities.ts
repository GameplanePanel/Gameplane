import { useQuery } from "@tanstack/react-query";
import { defineMessages } from "react-intl";
import { createRequestClient } from "./api";
import { useOptionalIntl } from "@/lib/i18n/format";
import { resourceKey, type ResourceTarget } from "./resourceTarget";

const MESSAGES = defineMessages({
  identity_changed: {
    id: "web.lib.use_server_capabilities.identity_changed",
    defaultMessage: "The server identity changed. Reload this server before continuing.",
    description: "Error shown when a server's capabilities response no longer matches the server being viewed",
  },
  capture_unavailable: {
    id: "web.lib.use_server_capabilities.capture_unavailable",
    defaultMessage: "Capture limits are unavailable for this location.",
    description: "Error shown when the server reports packet capture limits that are not usable",
  },
});

export interface CaptureCapabilities {
  enabled: boolean;
  files: boolean;
  state: "ready" | "unsupported" | "unavailable";
  defaultRetentionSeconds: number;
  maxRetentionSeconds: number;
  defaultMaxDurationSeconds: number;
  defaultMaxSizeBytes: number;
}

interface ServerCapabilities {
  target: ResourceTarget;
  capture: CaptureCapabilities;
}

export function useServerCapabilities(target: ResourceTarget, enabled: boolean) {
  const intl = useOptionalIntl();
  return useQuery({
    queryKey: resourceKey(target, "capabilities"),
    queryFn: async ({ signal }) => {
      const result = await createRequestClient(target, signal).api<ServerCapabilities>(
        `/servers/${encodeURIComponent(target.name)}/capabilities`,
      );
      if (result.target.cluster !== target.cluster || result.target.namespace !== target.namespace ||
          result.target.name !== target.name || (target.uid && result.target.uid !== target.uid)) {
        throw new Error(intl.formatMessage(MESSAGES.identity_changed));
      }
      const capture = result.capture;
      if (capture.state === "ready" && (
        ![capture.defaultRetentionSeconds, capture.maxRetentionSeconds, capture.defaultMaxDurationSeconds, capture.defaultMaxSizeBytes]
          .every((value) => Number.isSafeInteger(value) && value > 0) ||
        capture.defaultRetentionSeconds < 60 || capture.defaultRetentionSeconds > capture.maxRetentionSeconds || capture.defaultMaxDurationSeconds > 3600
      )) {
        throw new Error(intl.formatMessage(MESSAGES.capture_unavailable));
      }
      return result.capture;
    },
    enabled,
    retry: false,
    refetchInterval: 30_000,
  });
}
