// Unauthenticated surfaces (login page, public share links) must always
// render the modern Pink preset with zero per-user theme data (FR-011). A
// previous logged-in session on the same browser may have left theme <style>
// elements in the DOM, so this guard pins the root attributes and strips
// those elements for the route's lifetime — complementing the boot-time
// guard in index.html, which only covers the pre-paint window.

const THEME_STYLE_ELEMENT_IDS = [
  "gameplane-custom-css",
  "gameplane-custom-theme-vars",
  "gameplane-installed-theme-css",
] as const;

const PINNED_ATTRIBUTES = [
  ["data-theme-preset", "pink"],
  ["data-theme-type", "preset"],
  ["data-custom-css", "off"],
] as const;

/**
 * Enforce unauthenticated chrome on `document.documentElement` and keep
 * enforcing it until the returned disposer runs. Intended for a route-level
 * `useEffect(() => enforceUnauthenticatedTheme(), [])`. Light/dark state
 * (`data-theme`, the `dark` class) is deliberately untouched.
 */
export function enforceUnauthenticatedTheme(): () => void {
  const html = document.documentElement;

  const pin = () => {
    for (const [name, value] of PINNED_ATTRIBUTES) {
      if (html.getAttribute(name) !== value) {
        html.setAttribute(name, value);
      }
    }
    for (const id of THEME_STYLE_ELEMENT_IDS) {
      document.getElementById(id)?.remove();
    }
  };

  pin();

  // If anything flips the pinned attributes or re-adds the overlay while
  // this route is mounted, revert it. pin() only writes attributes that
  // drifted, so its own writes settle the observer instead of looping.
  const observer = new MutationObserver(pin);
  observer.observe(html, {
    attributes: true,
    attributeFilter: ["data-theme-preset", "data-theme-type", "data-custom-css"],
  });
  observer.observe(document.head, { childList: true });

  return () => observer.disconnect();
}
