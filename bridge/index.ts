import { initSpacemoltClients, listOwnedPlayers, listOwnedPlayersByKey, getConnectedAccount, removeConnectedAccount } from "./libClient.ts";
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
            const { filter, botIds, ids } = req.params || {};
            const playerIds = botIds ?? ids;
            const { connectOwnedAccounts } = await import("./libClient.ts");
            console.log(
              `[bridge] connectOwned playerIds=${JSON.stringify(playerIds)}`,
            );
            const accounts = await connectOwnedAccounts(
              (p: any) => (playerIds ? playerIds.includes(p.username || p.id) : true) && (filter ? filter(p) : true),
              (account: any) => {
                // push connected event
                console.log(
                  `[bridge] account.connected id=${account.id} username=${account.username}`,
                );
                ws.send(JSON.stringify({ jsonrpc: "2.0", method: "account.connected", params: { id: account.id, username: account.username, account: { id: account.id, username: account.username } } }));
                // listen disconnect
                account.onDisconnected?.((err: any) => {
                  ws.send(JSON.stringify({ jsonrpc: "2.0", method: "account.disconnected", params: { id: account.id, reason: String(err) } }));
                });
                // forward live state changes to Go dashboard
                account.onStateChange?.((_changed: any[]) => {
                  const st = account.state as any;
                  if (!st) return;
                  ws.send(JSON.stringify({ jsonrpc: "2.0", method: "account.status", params: {
                    id: account.id,
                    username: account.username,
                    credits: st.credits,
                    fuel: st.fuel,
                    maxFuel: st.max_fuel ?? st.maxFuel,
                    hull: st.hull,
                    maxHull: st.max_hull ?? st.maxHull,
                    shield: st.shield,
                    maxShield: st.max_shield ?? st.maxShield,
                    cargo: st.cargo,
                    isCloaked: st.is_cloaked,
                    system: st.location?.system_id,
                    poi: st.location?.poi_id,
                    location: st.location?.id || st.location?.name,
                    ship: st.ship?.name || st.ship?.id,
                  } }));
                });
              },
            );
            console.log(
              `[bridge] connectOwned done accounts=${accounts.map((a:any)=>a.id).join(",")}`,
            );
            reply(accounts.map((a: any) => ({ id: a.id, username: a.username })));
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
            console.log(
              `[bridge] exec id=${req.params?.id} command=${req.params?.command} accFound=${!!acc}`,
            );
            if (!acc) {
              reply(null, { code: -1, message: "account not found" });
              break;
            }
            const { command, params } = req.params || {};
            // command like "get_status" or tool/action? Support exec with command string
            let result;
            if (command && typeof acc.send === "function") {
              const parts = String(command).split(".");
              if (parts.length === 2) {
                result = await acc.send(parts[0], parts[1], params || {});
              } else {
                result = await acc.query("spacemolt", command, params || {});
              }
            }
            console.log(
              `[bridge] exec id=${req.params?.id} command=${req.params?.command} done`,
            );
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
