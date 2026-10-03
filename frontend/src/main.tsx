import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { Dialogs } from "./Dialog";
import "./style.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
    <Dialogs />
  </StrictMode>,
);
