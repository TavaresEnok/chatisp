import { api } from "@/api/client";

export type DownloadPackage = {
  id: string;
  title: string;
  description: string;
  filename: string;
  size: number;
  sha256: string;
};

export async function listPackages(signal?: AbortSignal): Promise<DownloadPackage[]> {
  const { data, response } = await api.GET("/api/panel/packages", { signal, cache: "no-store" });
  if (!response.ok || !data || !Array.isArray(data.packages)) throw new Error("Não foi possível carregar os pacotes.");
  return data.packages;
}
