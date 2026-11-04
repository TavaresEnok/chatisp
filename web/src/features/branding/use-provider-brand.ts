import { useQuery } from "@tanstack/react-query";
import { getBranding, getBrandingLogo, providerFallbackName, type ProviderIdentity } from "@/api/branding";
import { useSessionStore } from "@/stores/session-store";
import { brandingQueryKey } from "@/features/branding/cache";

export type ProviderBrand = {
  identity: ProviderIdentity;
  logo: Blob | null;
};

export function providerLabel(identity: ProviderIdentity | undefined): string {
  const fallback = identity?.fallback_name?.trim() || providerFallbackName;
  const name = identity?.display_name?.trim() || "";
  return name || fallback;
}

export function useProviderBrand() {
  const principal = useSessionStore((state) => state.principal);
  const enabled = principal !== null && !principal.mfaEnrollmentRequired;
  const tenantId = principal?.tenantId ?? "";
  const sessionId = principal?.sessionId ?? "";
  const query = useQuery({
    queryKey: enabled ? brandingQueryKey(tenantId, sessionId) : ["branding", "signed-out"],
    enabled,
    retry: false,
    queryFn: async ({ signal }): Promise<ProviderBrand> => {
      const identity = await getBranding(signal);
      if (!identity.has_logo) return { identity, logo: null };
      const logo = await getBrandingLogo(signal);
      return { identity, logo };
    },
  });
  return {
    name: providerLabel(query.data?.identity),
    logo: query.data?.logo ?? null,
    updatedAt: query.data?.identity.updated_at ?? "",
    hasLogo: query.data?.identity.has_logo === true,
    query,
  };
}
