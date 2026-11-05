import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { getErpCustomerLink, saveErpCustomerLink } from "@/api/erp";
import { errorMessage } from "@/api/problems";

// 08-abrir-cadastro: the provider's own ERP screen. The template is the
// provider's URL with {cliente}; the panel never puts a credential or a CPF in it.
export function CustomerLinkSettings({ scope }: { scope: string }) {
  const key = ["erp-customer-link", scope];
  const client = useQueryClient();
  const query = useQuery({ queryKey: key, queryFn: ({ signal }) => getErpCustomerLink(signal), retry: false });
  const [draft, setDraft] = useState<string | null>(null);
  const save = useMutation({
    mutationFn: (template: string) => saveErpCustomerLink(template, query.data?.version ?? 0),
    onSuccess: (data) => { client.setQueryData(key, data); setDraft(null); },
  });
  const value = draft ?? query.data?.url_template ?? "";
  function submit(event: FormEvent) {
    event.preventDefault();
    save.mutate(value.trim());
  }
  return (
    <section aria-label="Link do cadastro no ERP" className="mt-3 flex flex-col gap-2">
      <h4 className="text-sm font-medium">Link do cadastro no ERP</h4>
      <p>
        Endereço da tela de cliente no ERP que o provedor já usa, com <code>{"{cliente}"}</code> no lugar da referência do cliente.
        Só HTTPS; o link não aceita token, senha, CPF nem CNPJ. Referência que pareça CPF ou CNPJ não vira link.
      </p>
      {query.isPending ? <p>Carregando link…</p> : null}
      {query.isError ? <p role="alert">{errorMessage(query.error)}</p> : null}
      {query.isSuccess ? (
        <form onSubmit={submit} className="flex flex-col gap-2">
          <label className="flex min-w-0 flex-col gap-1">
            Modelo do link
            <input
              type="url"
              inputMode="url"
              autoComplete="off"
              placeholder="https://erp.exemplo/clientes/{cliente}"
              className="min-w-0 rounded border p-2"
              value={value}
              onChange={(event) => setDraft(event.target.value)}
            />
          </label>
          <div className="flex flex-wrap gap-2">
            <button type="submit" disabled={save.isPending}>Salvar link</button>
            {query.data.url_template ? (
              <button type="button" disabled={save.isPending} onClick={() => save.mutate("")}>Remover link</button>
            ) : null}
          </div>
          {save.isError ? <p role="alert">{errorMessage(save.error)}</p> : null}
          {save.isSuccess ? <p role="status">{save.data.url_template ? "Link salvo." : "Link removido."}</p> : null}
        </form>
      ) : null}
    </section>
  );
}
