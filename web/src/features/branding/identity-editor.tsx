import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { PanelRequestError } from "@/api/problems";
import { removeBrandingLogo, saveBrandingLogo, saveBrandingName } from "@/api/branding";
import { invalidateBranding } from "@/features/branding/cache";
import { BlobImage } from "@/features/branding/provider-mark";
import { useProviderBrand } from "@/features/branding/use-provider-brand";
import { useSessionStore } from "@/stores/session-store";

const maxBytes = 256 * 1024;

function localFileError(file: File): string | null {
  const name = file.name.toLowerCase();
  if (name.endsWith(".svg") || name.endsWith(".html") || name.endsWith(".htm") || file.type === "image/svg+xml" || file.type === "text/html") {
    return "Envie um PNG ou JPEG. SVG e HTML não são aceitos.";
  }
  if (file.type !== "image/png" && file.type !== "image/jpeg" && file.type !== "image/jpg") {
    return "Envie um PNG ou JPEG de até 256 KiB e 1024 por 1024 pixels.";
  }
  if (file.size < 1 || file.size > maxBytes) {
    return "O logo passa de 256 KiB ou está vazio.";
  }
  return null;
}

function logoContentType(file: File): "image/png" | "image/jpeg" | null {
  if (file.type === "image/png") return "image/png";
  if (file.type === "image/jpeg" || file.type === "image/jpg") return "image/jpeg";
  return null;
}

type WriteScope = {
  tenantId: string;
  sessionId: string;
  csrfToken: string | null;
};

function captureWriteScope(): WriteScope {
  const state = useSessionStore.getState();
  return {
    tenantId: state.principal?.tenantId ?? "",
    sessionId: state.principal?.sessionId ?? "",
    csrfToken: state.csrfToken,
  };
}

function sameWriteScope(scope: WriteScope): boolean {
  const state = useSessionStore.getState();
  return state.principal?.tenantId === scope.tenantId
    && state.principal?.sessionId === scope.sessionId
    && state.csrfToken === scope.csrfToken;
}

export function IdentityEditor() {
  const client = useQueryClient();
  const brand = useProviderBrand();
  const [name, setName] = useState<string | null>(null);
  const [file, setFile] = useState<File | null>(null);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const alive = useRef(true);
  const writeAbort = useRef<AbortController | null>(null);
  const writeGeneration = useRef(0);
  const currentName = name ?? brand.query.data?.identity.display_name ?? "";
  const shown = file ?? (brand.hasLogo ? brand.logo : null);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
      writeAbort.current?.abort();
    };
  }, []);

  function owns(scope: WriteScope) {
    return alive.current && sameWriteScope(scope);
  }

  async function refresh() {
    await invalidateBranding(client);
    await client.invalidateQueries({ queryKey: ["retention", "policy"] });
  }

  async function save() {
    setNotice("");
    setError("");
    if (file) {
      const problem = localFileError(file);
      if (problem) {
        setError(problem);
        return;
      }
    }
    const scope = captureWriteScope();
    const generation = writeGeneration.current + 1;
    writeGeneration.current = generation;
    const controller = new AbortController();
    writeAbort.current?.abort();
    writeAbort.current = controller;
    setBusy(true);
    let changed = false;
    try {
      if (!owns(scope)) return;
      await saveBrandingName(currentName.trim(), scope.csrfToken, controller.signal);
      if (!owns(scope) || controller.signal.aborted) return;
      changed = true;
      if (file) {
        const contentType = logoContentType(file);
        if (!contentType) {
          setError("Envie um PNG ou JPEG de até 256 KiB e 1024 por 1024 pixels.");
          return;
        }
        await saveBrandingLogo(file, contentType, scope.csrfToken, controller.signal);
        if (!owns(scope) || controller.signal.aborted) return;
      }
      setName(null);
      setFile(null);
      await refresh();
      if (!owns(scope)) return;
      setNotice("Identidade guardada. Os prazos de retenção não foram alterados.");
    } catch (caught) {
      if (!owns(scope) || controller.signal.aborted) return;
      if (changed) await refresh();
      setError(caught instanceof PanelRequestError && caught.status === 422
        ? "O arquivo ou o nome não foi aceito. Use PNG ou JPEG de até 256 KiB e 1024 por 1024 pixels."
        : "Não foi possível guardar a identidade.");
    } finally {
      if (writeGeneration.current === generation) {
        if (writeAbort.current === controller) writeAbort.current = null;
        if (alive.current) setBusy(false);
      }
    }
  }

  async function remove() {
    setNotice("");
    setError("");
    const scope = captureWriteScope();
    const generation = writeGeneration.current + 1;
    writeGeneration.current = generation;
    const controller = new AbortController();
    writeAbort.current?.abort();
    writeAbort.current = controller;
    setBusy(true);
    try {
      if (!owns(scope)) return;
      await removeBrandingLogo(scope.csrfToken, controller.signal);
      if (!owns(scope) || controller.signal.aborted) return;
      setFile(null);
      await refresh();
      if (!owns(scope)) return;
      setNotice("Logo removido. O nome e os prazos de retenção permanecem.");
    } catch {
      if (!owns(scope) || controller.signal.aborted) return;
      setError("Não foi possível remover o logo.");
    } finally {
      if (writeGeneration.current === generation) {
        if (writeAbort.current === controller) writeAbort.current = null;
        if (alive.current) setBusy(false);
      }
    }
  }

  return (
    <section aria-labelledby="identidade-provedor" className="flex flex-col gap-3">
      <h2 id="identidade-provedor" className="text-base font-medium">Identidade do provedor</h2>
      <p>O nome exibido é o mesmo da política de retenção. Aqui não se alteram prazos nem o expurgo, que continua desligado.</p>
      {brand.query.isPending ? <p role="status">Carregando a identidade do provedor…</p> : null}
      {brand.query.isError ? <p role="alert">Não foi possível carregar a identidade do provedor.</p> : null}
      {brand.query.isSuccess ? (
        <>
          <label>
            Nome exibido do provedor
            <input aria-label="Nome exibido do provedor" value={currentName} maxLength={80} onChange={(event) => setName(event.target.value)} />
          </label>
          <label>
            Logo (PNG ou JPEG, até 256 KiB e 1024 por 1024 pixels)
            <input
              aria-label="Logo do provedor"
              type="file"
              accept="image/png,image/jpeg"
              onChange={(event) => {
                const next = event.target.files?.[0] ?? null;
                setNotice("");
                setFile(next);
                setError(next ? localFileError(next) ?? "" : "");
              }}
            />
          </label>
          {shown ? <BlobImage blob={shown} alt="Prévia do logo do provedor" className="h-16 w-16 object-contain" /> : <p>Sem logo. O painel usa o nome ou Chat ISP.</p>}
          <button type="button" disabled={busy} onClick={() => void save()}>Guardar identidade</button>
          <button type="button" disabled={busy || (!brand.hasLogo && !file)} onClick={() => void remove()}>Remover logo</button>
        </>
      ) : null}
      {error ? <p role="alert">{error}</p> : null}
      {notice ? <p role="status">{notice}</p> : null}
    </section>
  );
}
