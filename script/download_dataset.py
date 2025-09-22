#!/usr/bin/env python3
"""
Dataset Download Script
Downloads CSV dataset from Google Drive if not already present in data folder
"""

import os
import requests
import re
import sys


def download_csv_file():
    """Download CSV file from Google Drive"""
    print('Downloading CSV file...')
    
    # Create data directory
    data_dir = 'data'
    os.makedirs(data_dir, exist_ok=True)
    
    csv_path = os.path.join(data_dir, 'len3_data.csv')
    
    # Check if the file already exists
    if os.path.exists(csv_path):
        print(f'CSV file already exists: {csv_path}')
        return True
    
    try:
        # Google Drive file ID
        file_id = '1gIBGcneoUz9jaU48PYCjP6xjWegRlgE-'
        
        # Create session
        session = requests.Session()
        direct_url = f'https://drive.google.com/uc?export=download&id={file_id}'
        
        print(f'Downloading from: {direct_url}')
        
        # First request to get the confirmation page
        response = session.get(direct_url, stream=True, timeout=30)
        response.raise_for_status()
        
        # Check if the virus scan warning page is encountered
        if 'Google Drive can\'t scan this file for viruses' in response.text:
            print('File requires virus scan confirmation. Getting download link...')
            
            # Extract the download form action URL
            form_match = re.search(r'action="([^"]*)"', response.text)
            if form_match:
                download_url = form_match.group(1)
                # Add form parameters
                download_url += f'?id={file_id}&export=download&confirm=t'
                
                print(f'Downloading from confirmation URL: {download_url}')
                response = session.get(download_url, stream=True, timeout=60)
                response.raise_for_status()
            else:
                raise Exception('Could not find download confirmation URL')
        
        # Save file
        with open(csv_path, 'wb') as f:
            for chunk in response.iter_content(chunk_size=8192):
                f.write(chunk)
        
        print(f'CSV file downloaded successfully: {csv_path}')
        return True
        
    except requests.exceptions.RequestException as e:
        print(f'Error downloading CSV file: {e}')
        print('Please manually download the file from:')
        print('https://drive.google.com/file/d/1gIBGcneoUz9jaU48PYCjP6xjWegRlgE-/view')
        print(f'And place it in: {csv_path}')
        return False
    except Exception as e:
        print(f'Unexpected error downloading CSV file: {e}')
        return False


def main():
    """Main function"""
    print("=== Dataset Download Script ===")
    
    success = download_csv_file()
    
    if success:
        print("Dataset download completed successfully!")
        return 0
    else:
        print("Dataset download failed!")
        return 1


if __name__ == "__main__":
    sys.exit(main())
