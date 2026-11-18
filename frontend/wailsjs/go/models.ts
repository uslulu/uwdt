export namespace backend {

	export class AddressRoute {
	    ip: string;
	    rules: string[];
	    route: string;
	    error: string;

	    static createFrom(source: any = {}) {
	        return new AddressRoute(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ip = source["ip"];
	        this.rules = source["rules"];
	        this.route = source["route"];
	        this.error = source["error"];
	    }
	}
export class ConnectParams {
	    peerAddr: string;
	    password: string;
	    hashes: string[];
	    deviceId?: string;
	    workers?: number;
	    captchaMode?: string;
	    obfsMode?: string;
	    fingerprint?: string;
	    turnTcp?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ConnectParams(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.peerAddr = source["peerAddr"];
	        this.password = source["password"];
	        this.hashes = source["hashes"];
	        this.deviceId = source["deviceId"];
	        this.workers = source["workers"];
	        this.captchaMode = source["captchaMode"];
	        this.obfsMode = source["obfsMode"];
	        this.fingerprint = source["fingerprint"];
	        this.turnTcp = source["turnTcp"];
	    }
	}
	export class ExcludeEntry {
	    source?: string;
	    value: string;
	    enabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ExcludeEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.source = source["source"];
	        this.value = source["value"];
	        this.enabled = source["enabled"];
	    }
	}
	export class ProfileData {
	    peer: string;
	    password: string;
	    hashes: string[];
	    workers?: number;
	    listen: string;
	    turn: string;
	    port: string;
	    device_id: string;
	    turn_tcp: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ProfileData(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.peer = source["peer"];
	        this.password = source["password"];
	        this.hashes = source["hashes"];
	        this.workers = source["workers"];
	        this.listen = source["listen"];
	        this.turn = source["turn"];
	        this.port = source["port"];
	        this.device_id = source["device_id"];
	        this.turn_tcp = source["turn_tcp"];
	    }
	}
	export class RussiaInfo {
	    enabled: boolean;
	    count: number;
	    updated: string;
	
	    static createFrom(source: any = {}) {
	        return new RussiaInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.count = source["count"];
	        this.updated = source["updated"];
	    }
	}
	export class SubServer {
	    name: string;
	    link: string;
	    transport: string;
	    security: string;
	    host: string;
	    port: number;
	
	    static createFrom(source: any = {}) {
	        return new SubServer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.link = source["link"];
	        this.transport = source["transport"];
	        this.security = source["security"];
	        this.host = source["host"];
	        this.port = source["port"];
	    }
	}
	export class Subscription {
	    url: string;
	    title: string;
	    servers: SubServer[];
	    skipped: number;
	
	    static createFrom(source: any = {}) {
	        return new Subscription(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.title = source["title"];
	        this.servers = this.convertValues(source["servers"], SubServer);
	        this.skipped = source["skipped"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class UpdateInfo {
	    available: boolean;
	    version: string;
	    url: string;
	    body: string;
	    page: string;
	
	    static createFrom(source: any = {}) {
	        return new UpdateInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.version = source["version"];
	        this.url = source["url"];
	        this.body = source["body"];
	        this.page = source["page"];
	    }
	}

}

