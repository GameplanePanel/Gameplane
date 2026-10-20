import { Compass } from "lucide-react";
import { Link } from "@tanstack/react-router";
import { useOptionalIntl } from "@/lib/i18n/format";

// Rendered inside the app shell for any URL no route claims. The auth guard
// lives in AppLayout (401 -> /login), so this page carries no cluster data.
export function NotFoundPage() {
  const intl = useOptionalIntl();
  return (
    <div className="flex h-full min-h-[60vh] items-center justify-center p-6">
      <div className="flex max-w-md flex-col items-center text-center">
        <div className="flex h-14 w-14 items-center justify-center rounded-full bg-muted/15 text-muted">
          <Compass className="h-7 w-7" aria-hidden="true" />
        </div>
        <h1 className="pt-4 text-xl font-semibold text-fg">{intl.formatMessage({ id: "web.routes.not_found.page_not_found", defaultMessage: "Page not found", description: "Not found: heading of the 404 page saying the address matches no page" })}</h1>
        <p className="pt-2 text-sm text-muted">{intl.formatMessage({ id: "web.routes.not_found.the_page_you_re_looking_for_doesn_t", defaultMessage: "The page you're looking for doesn't exist or has moved.", description: "Not found: explanation under the heading saying the page does not exist or has moved" })}</p>
        <Link
          to="/"
          className="mt-5 inline-block rounded-full bg-primary px-5 py-2.5 text-sm font-medium text-primary-fg hover:opacity-90"
        >{intl.formatMessage({ id: "web.routes.not_found.go_to_dashboard", defaultMessage: "Go to dashboard", description: "Not found: link styled as a button under the explanation that returns the user to the dashboard; keep it short" })}</Link>
      </div>
    </div>
  );
}
