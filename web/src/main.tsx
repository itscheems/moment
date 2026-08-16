import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import "inter-ui/inter-variable-latin.css";
import { App } from "./app/App";
import "./styles/global.css";

const root = document.getElementById("root");
if (!root) {
  throw new Error("Moment root element is missing");
}

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
