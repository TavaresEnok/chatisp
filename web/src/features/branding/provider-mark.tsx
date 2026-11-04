import { useEffect, useRef } from "react";
import { useProviderBrand } from "@/features/branding/use-provider-brand";

export function ProviderMark() {
  const brand = useProviderBrand();
  return (
    <div className="flex items-center gap-3 px-4 py-4">
      <BlobImage blob={brand.hasLogo ? brand.logo : null} alt={`Logo de ${brand.name}`} className="h-10 w-10 rounded-md object-contain" />
      <div>
        <p className="text-base font-semibold tracking-tight">{brand.name}</p>
        <p className="mt-1 text-sm text-muted-foreground">Atendimento do provedor</p>
      </div>
    </div>
  );
}

export function BlobImage({ blob, alt, className }: { blob: Blob | null; alt: string; className: string }) {
  const ref = useRef<HTMLImageElement>(null);
  useEffect(() => {
    const img = ref.current;
    if (!img || !blob) return undefined;
    const url = URL.createObjectURL(blob);
    img.src = url;
    return () => URL.revokeObjectURL(url);
  }, [blob]);
  if (!blob) return null;
  return <img ref={ref} alt={alt} className={className} />;
}
