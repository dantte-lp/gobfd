interfaces {
    ethernet eth1 {
        address "10.0.5.2/30"
    }
}

protocols {
    bgp {
        system-as "65006"
        parameters {
            router-id "10.0.5.2"
        }
        neighbor 10.0.5.1 {
            remote-as "65001"
            bfd {
            }
        }
        address-family {
            ipv4-unicast {
                network 10.20.5.0/24 {
                }
            }
        }
    }
    bfd {
        peer 10.0.5.1 {
            source {
                interface "eth1"
            }
            interval {
                receive-interval "300"
                transmit-interval "300"
                multiplier "3"
            }
        }
    }
    static {
        route 10.20.5.0/24 {
            blackhole {
            }
        }
    }
}
