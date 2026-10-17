"""one machine per node plus one machine per client, all on one LAN with fixed
addresses (node i is 10.10.1.i, client k is 10.10.1.(n+k), named client, client2, ...),
matching scripts/cluster/hosts.cloudlab.json for n = 7 and one client. A second client
host is for an emulated WAN with a client in each site
(scripts/cluster/hosts.cloudlab.wan_far67_2c.json).

Instructions:
Log in to `client` with `ssh -A` once to give it its own key on the nodes
(docs/multi-host.md, Bring-up step 2), clone the pbft repo there, then
scripts/cluster/setup.sh once and scripts/cluster/start.sh / stop.sh per run.
"""

import geni.portal as portal
import geni.rspec.pg as pg
# Emulab extensions; adds setNoBandwidthShaping to LAN/Link.
import geni.rspec.emulab as emulab

pc = portal.Context()
pc.defineParameter(
    "hwtype", "Hardware type (every machine gets the same one)",
    portal.ParameterType.NODETYPE, "c6320",
    longDescription="Mixed hardware would make the nodes differ in CPU speed, which the "
    "protocol's timers react to. Default c6320 (Clemson, 2x14 cores), the type of the "
    "development host; to compare with single-host runs, pick the type those ran on.")
pc.defineParameter(
    "nodeCount", "PBFT nodes (node_num)", portal.ParameterType.INTEGER, 7)
pc.defineParameter(
    "clientCount", "Client machines (client_num)", portal.ParameterType.INTEGER, 1,
    longDescription="1 for the LAN and the default emulated WAN; 2 for a client in each site of "
    "the emulated WAN. Client k is 10.10.1.(nodeCount+k).")
pc.defineParameter(
    "image", "Disk image", portal.ParameterType.IMAGE,
    "urn:publicid:IDN+emulab.net+image+emulab-ops//UBUNTU22-64-STD")
params = pc.bindParameters()
if params.nodeCount < 4 or params.nodeCount > 250:
    pc.reportError(portal.ParameterError("nodeCount must be in 4..250", ["nodeCount"]))
if params.clientCount < 1 or params.nodeCount + params.clientCount > 254:
    pc.reportError(portal.ParameterError("clientCount must be >= 1 and fit in 10.10.1.0/24",
                                         ["clientCount"]))
pc.verifyParameters()

request = pc.makeRequestRSpec()
lan = request.LAN("lan")
# No shaping: the experiments add their own delay with netem
# (scripts/netem_egress.sh); a shaped link would add a delay node in the path.
lan.setNoBandwidthShaping()


def add_machine(name, ip):
    machine = request.RawPC(name)
    machine.hardware_type = params.hwtype
    machine.disk_image = params.image
    iface = machine.addInterface("if1")
    iface.addAddress(pg.IPv4Address(ip, "255.255.255.0"))
    lan.addInterface(iface)


for i in range(1, params.nodeCount + 1):
    add_machine("node%d" % i, "10.10.1.%d" % i)
for k in range(1, params.clientCount + 1):
    add_machine("client" if k == 1 else "client%d" % k, "10.10.1.%d" % (params.nodeCount + k))

pc.printRequestRSpec(request)
