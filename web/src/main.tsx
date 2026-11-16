import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "@/app";
import { syncInterfaceToDocument } from "@/stores/interface-store";
import "./index.css";

syncInterfaceToDocument();

const rootElement = document.getElementById("root");
if (!rootElement) {
  throw new Error("Elemento raiz ausente.");
}

createRoot(rootElement).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
