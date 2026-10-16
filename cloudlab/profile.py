"""one machine per node plus one client
machine, all on one LAN with fixed addresses (node i is 10.10.1.i, the client is
10.10.1.(n+1)), matching scripts/cluster/hosts.cloudlab.json for n = 7.

Instructions:
Log in to `client` with `ssh -A` (it needs your key to reach the nodes), clone the
pbft repo there, and follow docs/multi-host.md: scripts/cluster/setup.sh once, then
scripts/cluster/start.sh / stop.sh per run.
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
    "image", "Disk image", portal.ParameterType.IMAGE,
    "urn:publicid:IDN+emulab.net+image+emulab-ops//UBUNTU22-64-STD")
params = pc.bindParameters()
if params.nodeCount < 4 or params.nodeCount > 250:
    pc.reportError(portal.ParameterError("nodeCount must be in 4..250", ["nodeCount"]))
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
add_machine("client", "10.10.1.%d" % (params.nodeCount + 1))

pc.printRequestRSpec(request)
