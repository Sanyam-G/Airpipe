// Shared wire-protocol + crypto helpers for all browser pages.
// Loaded before any page-specific script. Depends on nacl-fast.min.js.

const MSG_METADATA=0x01, MSG_READY=0x02, MSG_COMPLETE=0x03, MSG_ERROR=0x04;
const MSG_CHUNK=0x10, MSG_VERSION=0x20;
const MSG_SDP_OFFER=0x30, MSG_SDP_ANSWER=0x31, MSG_ICE_CANDIDATE=0x32;
const MSG_P2P_READY=0x33, MSG_P2P_FAIL=0x34, MSG_PEER_JOIN=0x35;
const MSG_SESSION_END=0x36;

const PROTOCOL_VERSION=4;

const ICE_SERVERS=[
  {urls:'stun:stun.l.google.com:19302'},
  {urls:'stun:stun1.l.google.com:19302'},
  {urls:'stun:stun.cloudflare.com:3478'}
];

function encode(type,payload){
  const r=new Uint8Array(5+payload.length);
  r[0]=type;
  r[1]=(payload.length>>24)&0xff;
  r[2]=(payload.length>>16)&0xff;
  r[3]=(payload.length>>8)&0xff;
  r[4]=payload.length&0xff;
  r.set(payload,5);
  return r;
}

function decode(data){
  if(data.length<5)return null;
  const type=data[0];
  const len=(data[1]<<24)|(data[2]<<16)|(data[3]<<8)|data[4];
  if(data.length<5+len)return null;
  return {type,payload:data.slice(5,5+len)};
}

function encrypt(data,key){
  const nonce=nacl.randomBytes(24);
  const enc=nacl.secretbox(data,nonce,key);
  const r=new Uint8Array(24+enc.length);
  r.set(nonce);
  r.set(enc,24);
  return r;
}

function decrypt(data,key){
  if(data.length<24)return null;
  const nonce=data.slice(0,24);
  const ct=data.slice(24);
  return nacl.secretbox.open(ct,nonce,key);
}

// Mirrors internal/passphrase.Derive: PBKDF2-HMAC-SHA256 once, then HKDF
// labels. Slow on purpose; call once per transfer. A copy lives in web/assets/app.js.
async function deriveSecrets(phrase){
  const s=crypto.subtle, te=new TextEncoder();
  const norm=phrase.toUpperCase().trim().replace(/\s+/g,' ');
  const pw=await s.importKey('raw',te.encode(norm),'PBKDF2',false,['deriveBits']);
  const master=await s.deriveBits({name:'PBKDF2',hash:'SHA-256',salt:te.encode('airpipe/v5/kdf'),iterations:600000},pw,256);
  const mk=await s.importKey('raw',master,'HKDF',false,['deriveBits']);
  const expand=async(label,n)=>new Uint8Array(await s.deriveBits({name:'HKDF',hash:'SHA-256',salt:new Uint8Array(),info:te.encode(label)},mk,n*8));
  const hex=b=>Array.from(b,x=>x.toString(16).padStart(2,'0')).join('');
  return {
    key:await expand('airpipe/v5/key',32),
    token:hex(await expand('airpipe/v5/token',16)),
    receiveToken:hex(await expand('airpipe/v5/receive-token',16)),
  };
}

function formatSize(bytes){
  if(bytes===0)return '0 B';
  const units=['B','KB','MB','GB'];
  const i=Math.floor(Math.log(bytes)/Math.log(1024));
  return (bytes/Math.pow(1024,i)).toFixed(i===0?0:1)+' '+units[i];
}
