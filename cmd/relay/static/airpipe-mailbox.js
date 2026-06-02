// AMB2 mailbox bundle encoder + decoder. Mirrors internal/mailbox/payload.go.

const MAILBOX_MAX_FILES=256;
const MAILBOX_MAX_NAME=4095;
const MAILBOX_MAX_PAYLOAD=500<<20;

function encodeMailboxV1(filename,content){
  const nb=new TextEncoder().encode(filename);
  const payload=new Uint8Array(4+nb.length+content.length);
  payload[0]=(nb.length>>24)&0xff;
  payload[1]=(nb.length>>16)&0xff;
  payload[2]=(nb.length>>8)&0xff;
  payload[3]=nb.length&0xff;
  payload.set(nb,4);
  payload.set(content,4+nb.length);
  return payload;
}

function encodeMailboxAMB2(entries){
  if(entries.length<1)throw new Error('mailbox: no entries');
  if(entries.length>MAILBOX_MAX_FILES)throw new Error('mailbox: too many files (max '+MAILBOX_MAX_FILES+')');
  const te=new TextEncoder();
  const names=[];
  let combined=0;
  for(const e of entries){
    const nb=te.encode(e.name||'file');
    if(nb.length<1||nb.length>MAILBOX_MAX_NAME)throw new Error('mailbox: invalid filename: '+(e.name||'?'));
    if(e.content.byteLength>MAILBOX_MAX_PAYLOAD)throw new Error('mailbox: file too large: '+e.name);
    combined+=e.content.byteLength;
    if(combined>MAILBOX_MAX_PAYLOAD)throw new Error('mailbox: combined size too large');
    names.push(nb);
  }
  let total=8;
  for(let i=0;i<entries.length;i++){
    total+=4+names[i].length+8+entries[i].content.byteLength;
  }
  const out=new Uint8Array(total);
  out[0]=0x41;out[1]=0x4D;out[2]=0x42;out[3]=0x32;
  const dv=new DataView(out.buffer);
  dv.setUint32(4,entries.length);
  let o=8;
  for(let i=0;i<entries.length;i++){
    const nb=names[i];
    dv.setUint32(o,nb.length);o+=4;
    out.set(nb,o);o+=nb.length;
    dv.setBigUint64(o,BigInt(entries[i].content.byteLength));o+=8;
    out.set(entries[i].content,o);o+=entries[i].content.byteLength;
  }
  return out;
}

function decodeMailboxAMB2(rest){
  if(rest.length<4)throw new Error('invalid bundle');
  const dv=new DataView(rest.buffer,rest.byteOffset,rest.byteLength);
  let o=0;
  const n=dv.getUint32(o);o+=4;
  if(n<1||n>MAILBOX_MAX_FILES)throw new Error('invalid bundle');
  const out=[];
  const td=new TextDecoder();
  for(let i=0;i<n;i++){
    if(o+4>rest.length)throw new Error('invalid bundle');
    const fnLen=dv.getUint32(o);o+=4;
    if(fnLen<1||fnLen>MAILBOX_MAX_NAME||o+fnLen+8>rest.length)throw new Error('invalid bundle');
    const name=td.decode(rest.subarray(o,o+fnLen));o+=fnLen;
    const sz=Number(dv.getBigUint64(o));o+=8;
    if(!Number.isFinite(sz)||sz<0||o+sz>rest.length)throw new Error('invalid bundle');
    out.push({name,content:rest.slice(o,o+sz)});
    o+=sz;
  }
  if(o!==rest.byteLength)throw new Error('invalid bundle');
  return out;
}

function decodeMailboxEntries(decrypted){
  if(decrypted.length<4)throw new Error('invalid payload');
  const head=String.fromCharCode(decrypted[0],decrypted[1],decrypted[2],decrypted[3]);
  if(head==='AMB2')return decodeMailboxAMB2(decrypted.slice(4));
  const fnLen=(decrypted[0]<<24)|(decrypted[1]<<16)|(decrypted[2]<<8)|decrypted[3];
  if(fnLen<1||fnLen>MAILBOX_MAX_NAME||4+fnLen>decrypted.length)throw new Error('invalid payload');
  const name=new TextDecoder().decode(decrypted.slice(4,4+fnLen));
  const content=decrypted.slice(4+fnLen);
  return [{name,content}];
}
