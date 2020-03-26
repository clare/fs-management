"use strict";

let snapshotCount = 0;
let snapshotLimit = 200;
let image;
let status;
let statusMessage;

window.onload = async function() {
  image = document.getElementById("snapshot-image");
  status = document.getElementById("snapshot-stopped");
  statusMessage = document.getElementById("snapshot-stopped-message");
  const urlParams = new URLSearchParams(window.location.search);
  if(urlParams.get('timeout') === "off")  {
    snapshotLimit = Number.MAX_SAFE_INTEGER;
  }
  await updateSnapshotLoop();
};

async function restartCameraViewing() {
  showImage();
  snapshotCount = 0;
  await updateSnapshotLoop();
}

async function updateSnapshotLoop() {
  try {
    await fetch({
      method: 'PUT',
      headers: {
        "Authorization": `Basic ${btoa("admin:feathers")}`
      }
    });
    image.src = `/camera/snapshot?${new Date().getTime()}`;
    snapshotCount++;
    if (snapshotCount < snapshotLimit) {
      setTimeout(updateSnapshotLoop, 500);
    } else {
      stopSnapshots('Timeout for camera viewing.');
    }
  } catch(err) {
    console.log('error:', err);
    stopSnapshots('Error getting new snapshot');
  }
}

function showImage() {
  status.style.display = 'none';
  image.style.display = 'block';
}

function hideImage() {
  status.style.display = 'block';
  image.style.display = 'none';
}

function stopSnapshots(message) {
  statusMessage.innerText = message;
  hideImage();
}
