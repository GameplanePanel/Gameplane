import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";

export interface Installation {
  standalone: boolean;
  localCluster: boolean;
}

export function useInstallation() {
  return useQuery({
    queryKey: ["installation"],
    queryFn: () => api<Installation>("/admin/installation"),
    staleTime: Infinity,
  });
}
