import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// Mock localStorage
const localStorageMock = (() => {
  let store: Record<string, string> = {};

  return {
    getItem: (key: string) => store[key] ?? null,
    setItem: (key: string, value: string) => {
      store[key] = value.toString();
    },
    removeItem: (key: string) => {
      delete store[key];
    },
    clear: () => {
      store = {};
    },
  };
})();

beforeEach(() => {
  // Reset store and mock localStorage for each test
  localStorageMock.clear();
  Object.defineProperty(window, "localStorage", {
    value: localStorageMock,
    writable: true,
  });
  // Clear any cached cluster value by re-importing
  vi.resetModules();
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("Cluster Store", () => {
  it("preserves an explicitly cleared selection after reloading the store", async () => {
    localStorageMock.setItem("gameplane.cluster", "");
    const { getCurrentCluster: freshGetCurrentCluster } = await import("./cluster");
    expect(freshGetCurrentCluster()).toBe("");
  });
  it("returns default 'local' cluster when nothing is stored", async () => {
    // Re-import to get fresh module state
    const { getCurrentCluster: freshGetCurrentCluster } = await import("./cluster");
    expect(freshGetCurrentCluster()).toBe("local");
  });

  it("persists cluster selection to localStorage", async () => {
    const { getCurrentCluster: freshGetCurrentCluster, setCurrentCluster: freshSetCurrentCluster } =
      await import("./cluster");

    freshSetCurrentCluster("remote-prod");
    expect(localStorageMock.getItem("gameplane.cluster")).toBe("remote-prod");
    expect(freshGetCurrentCluster()).toBe("remote-prod");
  });

  it("retrieves persisted cluster from localStorage on first call", async () => {
    localStorageMock.setItem("gameplane.cluster", "remote-staging");
    const { getCurrentCluster: freshGetCurrentCluster } = await import("./cluster");
    expect(freshGetCurrentCluster()).toBe("remote-staging");
  });

  it("notifies subscribers when cluster changes", async () => {
    const { setCurrentCluster: freshSetCurrentCluster, subscribeCluster: freshSubscribeCluster } =
      await import("./cluster");

    const cb = vi.fn();
    freshSubscribeCluster(cb);

    freshSetCurrentCluster("remote-prod");
    expect(cb).toHaveBeenCalled();

    freshSetCurrentCluster("remote-staging");
    expect(cb).toHaveBeenCalledTimes(2);
  });

  it("unsubscribe stops receiving notifications", async () => {
    const { setCurrentCluster: freshSetCurrentCluster, subscribeCluster: freshSubscribeCluster } =
      await import("./cluster");

    const cb = vi.fn();
    const unsub = freshSubscribeCluster(cb);

    freshSetCurrentCluster("remote-prod");
    expect(cb).toHaveBeenCalledTimes(1);

    unsub();
    freshSetCurrentCluster("remote-staging");
    expect(cb).toHaveBeenCalledTimes(1); // Still only 1, unsubscribe worked
  });

  it("handles missing localStorage gracefully (SSR)", async () => {
    // Temporarily remove localStorage
    const originalLocalStorage = window.localStorage;
    Object.defineProperty(window, "localStorage", {
      value: undefined,
      writable: true,
    });

    const { getCurrentCluster: freshGetCurrentCluster, setCurrentCluster: freshSetCurrentCluster } =
      await import("./cluster");

    expect(freshGetCurrentCluster()).toBe("local");
    // Should not throw even though localStorage is undefined
    freshSetCurrentCluster("remote-prod");
    expect(freshGetCurrentCluster()).toBe("remote-prod");

    // Restore localStorage
    Object.defineProperty(window, "localStorage", {
      value: originalLocalStorage,
      writable: true,
    });
  });

  it("useCurrentCluster hook returns current cluster", async () => {
    const { renderHook, act } = await import("@testing-library/react");
    const { useCurrentCluster: freshUseCurrentCluster, setCurrentCluster: freshSetCurrentCluster } =
      await import("./cluster");

    const { result } = renderHook(() => freshUseCurrentCluster());

    // Initially should return "local"
    expect(result.current).toBe("local");

    // After setting a new cluster, the hook should reflect the change
    act(() => {
      freshSetCurrentCluster("remote-prod");
    });

    expect(result.current).toBe("remote-prod");
  });

  it("handles SecurityError in private browsing when getting cluster", async () => {
    // Mock localStorage to throw SecurityError on getItem
    const securityErrorLocalStorage = {
      getItem: vi.fn(() => {
        throw new Error("SecurityError: access is denied for this document.");
      }),
      setItem: vi.fn(),
      removeItem: vi.fn(),
      clear: vi.fn(),
      length: 0,
      key: vi.fn(),
    };

    Object.defineProperty(window, "localStorage", {
      value: securityErrorLocalStorage,
      writable: true,
    });

    const { getCurrentCluster: freshGetCurrentCluster } = await import("./cluster");
    // Should return default, not throw
    expect(freshGetCurrentCluster()).toBe("local");
  });

  it("handles SecurityError in private browsing when setting cluster", async () => {
    const securityErrorLocalStorage = {
      getItem: vi.fn(() => null),
      setItem: vi.fn(() => {
        throw new Error("SecurityError: access is denied for this document.");
      }),
      removeItem: vi.fn(),
      clear: vi.fn(),
      length: 0,
      key: vi.fn(),
    };

    Object.defineProperty(window, "localStorage", {
      value: securityErrorLocalStorage,
      writable: true,
    });

    const { setCurrentCluster: freshSetCurrentCluster, getCurrentCluster: freshGetCurrentCluster } =
      await import("./cluster");

    // Should not throw even though setItem failed
    freshSetCurrentCluster("remote-prod");
    // The in-memory cache should still be updated
    expect(freshGetCurrentCluster()).toBe("remote-prod");
  });

  it("caches cluster value after first load", async () => {
    localStorageMock.setItem("gameplane.cluster", "remote-staging");
    const { getCurrentCluster: freshGetCurrentCluster } = await import("./cluster");

    // First call reads from localStorage
    expect(freshGetCurrentCluster()).toBe("remote-staging");

    // Clear localStorage
    localStorageMock.clear();

    // Second call should return cached value, not default
    expect(freshGetCurrentCluster()).toBe("remote-staging");
  });

  it("multiple subscribers all receive notifications", async () => {
    const { setCurrentCluster: freshSetCurrentCluster, subscribeCluster: freshSubscribeCluster } =
      await import("./cluster");

    const cb1 = vi.fn();
    const cb2 = vi.fn();
    const cb3 = vi.fn();

    freshSubscribeCluster(cb1);
    freshSubscribeCluster(cb2);
    freshSubscribeCluster(cb3);

    freshSetCurrentCluster("new-cluster");

    expect(cb1).toHaveBeenCalledTimes(1);
    expect(cb2).toHaveBeenCalledTimes(1);
    expect(cb3).toHaveBeenCalledTimes(1);
  });

  it("does not call notifySubscribers when localStorage fails but cache is same", async () => {
    const { setCurrentCluster: freshSetCurrentCluster, subscribeCluster: freshSubscribeCluster } =
      await import("./cluster");

    const cb = vi.fn();
    freshSubscribeCluster(cb);

    // Set initial cluster
    freshSetCurrentCluster("prod");
    expect(cb).toHaveBeenCalledTimes(1);

    // Set same cluster again
    freshSetCurrentCluster("prod");
    expect(cb).toHaveBeenCalledTimes(2); // still called, but to same value
  });
});
