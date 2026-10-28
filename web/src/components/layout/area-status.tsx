import {
  Card,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

type AreaStatusProps = {
  status: "signed-out" | "loading" | "error" | "empty";
  title: string;
  description: string;
};

export function AreaStatus({ status, title, description }: AreaStatusProps) {
  if (status === "loading") {
    return (
      <div role="status" className="text-sm text-foreground">
        Carregando.
      </div>
    );
  }

  if (status === "error") {
    return (
      <div role="alert">
        <Card>
          <CardHeader>
            <CardTitle>
              <h2>Não foi possível carregar</h2>
            </CardTitle>
            <CardDescription>
              O servidor não respondeu. Nada foi inventado para preencher esta
              área.
            </CardDescription>
          </CardHeader>
        </Card>
      </div>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h2>{title}</h2>
        </CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
    </Card>
  );
}
