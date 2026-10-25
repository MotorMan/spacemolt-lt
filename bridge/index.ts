import { initSpacemoltClients, listOwnedPlayers, listOwnedPlayersByKey, getConnectedAccount, removeConnectedAccount } from "../../spacemolt_botrunner/src/libClient.ts";
import { SpacemoltClient } from "@spacemolt/lib";

const PORT = Number(process.env.PORT || 8787);

type RpcReq = { jsonrpc: string; id?: number; method: string; params?: any };

const server = Bun.serve({
  port: PORT,
  fetch(req, srv) {
    if (srv.upgrade(req)) return;
    return new Response("bridge ok");
  },
  websocket: {
    open(ws) {
      (ws as any)._subs = [];
    },
    async message(ws, raw) {
      let req: RpcReq;
      try { req = JSON.parse(String(raw)); } catch { return; }
      const reply = (result?: any, error?: any) => {
        ws.send(JSON.stringify({ jsonrpc: "2.0", id: req.id, result, error }));
      };
      try {
        switch (req.method) {
          case "initClients": {
            initSpacemoltClients(req.params?.keys ?? []);
            reply({ ok: true });
            break;
          }
          case "listOwnedPlayers": {
            const players = await listOwnedPlayers();
            reply(players);
            break;
          }
          case "listOwnedPlayersByKey": {
            const groups = await listOwnedPlayersByKey();
            reply(groups);
            break;
          }
          case "connectOwned": {
            const { filter, botIds } = req.params || {};
            const clients = (globalThis as any).__clients ?? [];
            // use libClient connectOwnedAccounts
            const { connectOwnedAccounts } = await import("../../spacemolt_botrunner/src/libClient.ts");
            const accounts = await connectOwnedAccounts(
              (p: any) => (botIds ? botIds.includes(p.username || p.id) : true) && (filter ? filter(p) : true),
              (account: any) => {
                // push connected event
                ws.send(JSON.stringify({ jsonrpc: "2.0", method: "account.connected", params: { id: account.id, account: { id: account.id } } }));
                // listen disconnect
                account.onDisconnected?.((err: any) => {
                  ws.send(JSON.stringify({ jsonrpc: "2.0", method: "account.disconnected", params: { id: account.id, reason: String(err) } }));
                });
              },
            );
            reply(accounts.map((a: any) => ({ id: a.id })));
            break;
          }
          case "getAccount": {
            const acc = getConnectedAccount(req.params?.id);
            if (!acc) { reply(null, { code: -1, message: "not found" }); break; }
            reply({ id: acc.id, location: acc.location, credits: acc.credits });
            break;
          }
          case "exec": {
            const acc = getConnectedAccount(req.params?.id);
            if (!acc) { reply(null, { code: -1, message: "not found" }); break; }
            const { command, params } = req.params || {};
            // command like "get_status" or tool/action? Support exec with command string
            let result;
            if (command && typeof acc.send === "function") {
              // assume command is "tool.action"? fallback: treat as query tool spacemolt
              const parts = String(command).split(".");
              if (parts.length === 2) {
                result = await acc.send(parts[0], parts[1], params || {});
              } else {
                result = await acc.query("spacemolt", command, params || {});
              }
            }
            reply(result);
            break;
          }
          case "removeAccount": {
            removeConnectedAccount(req.params?.id);
            reply({ ok: true });
            break;
          }
          case "closeAccount": {
            const acc = getConnectedAccount(req.params?.id);
            if (acc) acc.close();
            removeConnectedAccount(req.params?.id);
            reply({ ok: true });
            break;
          }
          default:
            reply(null, { code: -2, message: "unknown method" });
        }
      } catch (e: any) {
        reply(null, { code: -3, message: String(e?.message || e) });
      }
    },
  },
});

console.log(`bridge listening on ${PORT}`);
