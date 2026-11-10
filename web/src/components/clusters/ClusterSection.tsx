import type { ReactNode } from "react";
import { Card, TextField, Label, Description } from "@heroui/react";
import { cn } from "@/lib/utils";

export function ClusterSection({
  title,
  subtitle,
  footer,
  children,
  className,
  headerClassName,
  titleClassName,
  subtitleClassName,
  contentClassName,
  footerClassName,
}: {
  title: string;
  subtitle?: string;
  footer?: ReactNode;
  children: ReactNode;
  className?: string;
  headerClassName?: string;
  titleClassName?: string;
  subtitleClassName?: string;
  contentClassName?: string;
  footerClassName?: string;
}) {
  return (
    <Card role="region" aria-label={title} className={cn("space-y-4", className)}>
      <Card.Header className={cn("space-y-2 pb-2", headerClassName)}>
        <div className={cn("font-medium text-base", titleClassName)}>{title}</div>
        {subtitle && <div className={cn("text-xs text-muted", subtitleClassName)}>{subtitle}</div>}
      </Card.Header>
      <Card.Content className={cn("space-y-4", contentClassName)}>{children}</Card.Content>
      {footer && (
        <Card.Footer className={cn("flex items-center justify-end gap-3 pt-2", footerClassName)}>
          {footer}
        </Card.Footer>
      )}
    </Card>
  );
}

export function ClusterField({ label, children, hint }: { label: string; children: ReactNode; hint?: string }) {
  return (
    <TextField className="space-y-1.5">
      <Label className="text-xs">{label}</Label>
      {children}
      {hint && <Description className="text-[11px]">{hint}</Description>}
    </TextField>
  );
}

